package parser

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/robfig/cron/v3"
)

const DefaultTimezone = "UTC"

type File struct {
	Timezone string
	Location *time.Location
	Jobs     []Job
}

type Job struct {
	ID        string
	Line      int
	Schedule  string
	CronSpecs []string
	Timezone  string
	CWD       string
	Prompt    string
	Raw       string
}

type ParseErrors []error

type token struct {
	value  string
	start  int
	end    int
	quoted bool
}

func (errs ParseErrors) Error() string {
	lines := make([]string, 0, len(errs))
	for _, err := range errs {
		lines = append(lines, err.Error())
	}
	return strings.Join(lines, "\n")
}

func Parse(content string) (File, error) {
	location, err := loadTimezone(DefaultTimezone)
	if err != nil {
		return File{}, err
	}
	file := File{
		Timezone: DefaultTimezone,
		Location: location,
	}

	var errs ParseErrors
	seenTimezone := false
	seenJob := false

	lines := strings.Split(content, "\n")
	for index, raw := range lines {
		lineNumber := index + 1
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if isTimezoneDirective(trimmed) {
			if seenJob {
				errs = append(errs, lineErr(lineNumber, "timezone must appear before jobs"))
				continue
			}
			if seenTimezone {
				errs = append(errs, lineErr(lineNumber, "timezone is already set"))
				continue
			}
			timezone, location, err := parseTimezoneDirective(trimmed)
			if err != nil {
				errs = append(errs, lineErr(lineNumber, err.Error()))
				continue
			}
			file.Timezone = timezone
			file.Location = location
			seenTimezone = true
			continue
		}

		seenJob = true
		job, err := parseLine(lineNumber, trimmed, file.Timezone)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		file.Jobs = append(file.Jobs, job)
	}

	if len(errs) > 0 {
		return file, errs
	}
	return file, nil
}

func ParseFile(content string) ([]Job, error) {
	file, err := Parse(content)
	return file.Jobs, err
}

func FindJob(jobs []Job, id string) (Job, error) {
	var matches []Job
	for _, job := range jobs {
		if job.ID == id || strings.HasPrefix(job.ID, id) {
			matches = append(matches, job)
		}
	}
	if len(matches) == 0 {
		return Job{}, fmt.Errorf("job not found: %s", id)
	}
	if len(matches) > 1 {
		return Job{}, fmt.Errorf("job id prefix is ambiguous: %s", id)
	}
	return matches[0], nil
}

func parseLine(lineNumber int, line string, timezone string) (Job, error) {
	schedule, cwd, prompt, err := splitJobLine(line)
	if err != nil {
		return Job{}, lineErr(lineNumber, err.Error())
	}

	cronSpecs, err := compileSchedule(schedule)
	if err != nil {
		return Job{}, lineErr(lineNumber, err.Error())
	}

	expanded, err := expandCWD(cwd)
	if err != nil {
		return Job{}, lineErr(lineNumber, err.Error())
	}

	return Job{
		ID:        jobID(lineNumber, schedule, expanded, prompt),
		Line:      lineNumber,
		Schedule:  schedule,
		CronSpecs: cronSpecs,
		Timezone:  timezone,
		CWD:       expanded,
		Prompt:    prompt,
		Raw:       line,
	}, nil
}

func isTimezoneDirective(line string) bool {
	first, _ := readBare(line)
	return strings.EqualFold(first, "timezone")
}

func parseTimezoneDirective(line string) (string, *time.Location, error) {
	first, rest := readBare(line)
	if !strings.EqualFold(first, "timezone") {
		return "", nil, errors.New("expected timezone directive")
	}

	name, rest := readBare(rest)
	if name == "" {
		return "", nil, errors.New("expected timezone <IANA name>")
	}
	if strings.TrimSpace(rest) != "" {
		return "", nil, errors.New("timezone accepts exactly one value")
	}

	location, err := loadTimezone(name)
	if err != nil {
		return "", nil, err
	}
	return location.String(), location, nil
}

func loadTimezone(name string) (*time.Location, error) {
	if strings.EqualFold(name, "utc") {
		return time.UTC, nil
	}
	if location, err := time.LoadLocation(name); err == nil {
		return location, nil
	}
	candidate := canonicalTimezoneCandidate(name)
	if candidate != name {
		if location, err := time.LoadLocation(candidate); err == nil {
			return location, nil
		}
	}
	return nil, fmt.Errorf("invalid timezone %q", name)
}

func canonicalTimezoneCandidate(name string) string {
	parts := strings.Split(name, "/")
	for i, part := range parts {
		pieces := strings.Split(part, "_")
		for j, piece := range pieces {
			if piece == "" {
				continue
			}
			lower := strings.ToLower(piece)
			pieces[j] = strings.ToUpper(lower[:1]) + lower[1:]
		}
		parts[i] = strings.Join(pieces, "_")
	}
	return strings.Join(parts, "/")
}

func splitJobLine(line string) (string, string, string, error) {
	tokens, err := scanTokens(line)
	if err != nil {
		return "", "", "", err
	}
	if len(tokens) == 0 {
		return "", "", "", errors.New("expected <when> <cwd> \"<prompt>\"")
	}

	cwdIndex := -1
	for i, tok := range tokens {
		if isCWDToken(tok.value) {
			cwdIndex = i
			break
		}
	}
	if cwdIndex <= 0 {
		return "", "", "", errors.New("expected <when> <cwd> \"<prompt>\"")
	}
	if cwdIndex+1 >= len(tokens) {
		return "", "", "", errors.New("expected quoted prompt after cwd")
	}

	promptToken := tokens[cwdIndex+1]
	if !promptToken.quoted {
		return "", "", "", errors.New("prompt must be quoted")
	}

	trailing := strings.TrimSpace(line[promptToken.end:])
	if trailing != "" && !strings.HasPrefix(trailing, "#") {
		return "", "", "", fmt.Errorf("unexpected trailing text after prompt: %s", trailing)
	}

	schedule := strings.Join(strings.Fields(line[:tokens[cwdIndex].start]), " ")
	if schedule == "" {
		return "", "", "", errors.New("expected schedule before cwd")
	}

	return schedule, tokens[cwdIndex].value, promptToken.value, nil
}

func scanTokens(input string) ([]token, error) {
	var tokens []token
	i := 0
	for i < len(input) {
		for i < len(input) && unicode.IsSpace(rune(input[i])) {
			i++
		}
		if i >= len(input) {
			break
		}

		start := i
		if input[i] == '"' {
			value, end, err := readQuoted(input, i)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, token{value: value, start: start, end: end, quoted: true})
			i = end
			continue
		}

		for i < len(input) && !unicode.IsSpace(rune(input[i])) {
			i++
		}
		tokens = append(tokens, token{value: input[start:i], start: start, end: i})
	}
	return tokens, nil
}

func readQuoted(input string, start int) (string, int, error) {
	var out strings.Builder
	escaped := false
	for i := start + 1; i < len(input); i++ {
		ch := input[i]
		if escaped {
			switch ch {
			case '"', '\\':
				out.WriteByte(ch)
			case 'n':
				out.WriteByte('\n')
			case 't':
				out.WriteByte('\t')
			default:
				out.WriteByte(ch)
			}
			escaped = false
			continue
		}
		switch ch {
		case '\\':
			escaped = true
		case '"':
			return out.String(), i + 1, nil
		default:
			out.WriteByte(ch)
		}
	}
	return "", len(input), errors.New("unterminated quoted string")
}

func isCWDToken(value string) bool {
	return value == "~" || strings.HasPrefix(value, "~/") || filepath.IsAbs(value)
}

func compileSchedule(input string) ([]string, error) {
	parts := strings.Fields(input)
	if len(parts) < 2 {
		return nil, errors.New("expected schedule like `daily 11am`")
	}

	frequency := strings.ToLower(parts[0])
	daySpec, err := compileDaySpec(frequency)
	if err != nil {
		return nil, err
	}

	timesText := strings.TrimSpace(strings.TrimPrefix(input, parts[0]))
	times, err := parseTimeList(timesText)
	if err != nil {
		return nil, err
	}

	specs := make([]string, 0, len(times))
	seen := map[string]bool{}
	cronParser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	for _, clock := range times {
		spec := fmt.Sprintf("%d %d * * %s", clock.minute, clock.hour, daySpec)
		if seen[spec] {
			continue
		}
		if _, err := cronParser.Parse(spec); err != nil {
			return nil, fmt.Errorf("compiled invalid schedule %q: %w", spec, err)
		}
		specs = append(specs, spec)
		seen[spec] = true
	}

	return specs, nil
}

func compileDaySpec(frequency string) (string, error) {
	switch frequency {
	case "daily", "day", "days":
		return "*", nil
	case "weekday", "weekdays":
		return "1-5", nil
	case "weekend", "weekends":
		return "0,6", nil
	case "sunday", "sundays":
		return "0", nil
	case "monday", "mondays":
		return "1", nil
	case "tuesday", "tuesdays":
		return "2", nil
	case "wednesday", "wednesdays":
		return "3", nil
	case "thursday", "thursdays":
		return "4", nil
	case "friday", "fridays":
		return "5", nil
	case "saturday", "saturdays":
		return "6", nil
	default:
		return "", fmt.Errorf("unknown schedule %q; use daily, weekdays, weekends, or a weekday name", frequency)
	}
}

type clockTime struct {
	hour   int
	minute int
}

func parseTimeList(input string) ([]clockTime, error) {
	normalized := strings.ReplaceAll(input, ",", " ")
	fields := strings.Fields(normalized)
	if len(fields) == 0 {
		return nil, errors.New("expected at least one time")
	}

	times := make([]clockTime, 0, len(fields))
	for _, field := range fields {
		clock, err := parseClock(field)
		if err != nil {
			return nil, err
		}
		times = append(times, clock)
	}
	return times, nil
}

func parseClock(input string) (clockTime, error) {
	raw := strings.TrimSpace(strings.ToLower(input))
	if raw == "" {
		return clockTime{}, errors.New("empty time")
	}

	suffix := ""
	switch {
	case strings.HasSuffix(raw, "am"):
		suffix = "am"
		raw = strings.TrimSuffix(raw, "am")
	case strings.HasSuffix(raw, "pm"):
		suffix = "pm"
		raw = strings.TrimSuffix(raw, "pm")
	}
	if raw == "" {
		return clockTime{}, fmt.Errorf("invalid time %q", input)
	}

	hourText := raw
	minuteText := "0"
	if strings.Contains(raw, ":") {
		parts := strings.Split(raw, ":")
		if len(parts) != 2 {
			return clockTime{}, fmt.Errorf("invalid time %q", input)
		}
		hourText = parts[0]
		minuteText = parts[1]
	}

	hour, err := strconv.Atoi(hourText)
	if err != nil {
		return clockTime{}, fmt.Errorf("invalid time %q", input)
	}
	minute, err := strconv.Atoi(minuteText)
	if err != nil {
		return clockTime{}, fmt.Errorf("invalid time %q", input)
	}
	if minute < 0 || minute > 59 {
		return clockTime{}, fmt.Errorf("invalid minute in %q", input)
	}

	if suffix != "" {
		if hour < 1 || hour > 12 {
			return clockTime{}, fmt.Errorf("invalid 12-hour time %q", input)
		}
		if suffix == "am" {
			if hour == 12 {
				hour = 0
			}
		} else if hour != 12 {
			hour += 12
		}
		return clockTime{hour: hour, minute: minute}, nil
	}

	if hour < 0 || hour > 23 {
		return clockTime{}, fmt.Errorf("invalid 24-hour time %q", input)
	}
	return clockTime{hour: hour, minute: minute}, nil
}

func readBare(input string) (string, string) {
	input = strings.TrimLeftFunc(input, unicode.IsSpace)
	if input == "" {
		return "", ""
	}
	for i, r := range input {
		if unicode.IsSpace(r) {
			return input[:i], input[i:]
		}
	}
	return input, ""
}

func expandCWD(cwd string) (string, error) {
	if cwd == "~" || strings.HasPrefix(cwd, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if cwd == "~" {
			cwd = home
		} else {
			cwd = filepath.Join(home, strings.TrimPrefix(cwd, "~/"))
		}
	}

	clean := filepath.Clean(cwd)
	if !filepath.IsAbs(clean) {
		return "", errors.New("cwd must be absolute or start with ~")
	}
	return clean, nil
}

func jobID(line int, schedule, cwd, prompt string) string {
	sum := sha1.Sum([]byte(fmt.Sprintf("%d\x00%s\x00%s\x00%s", line, schedule, cwd, prompt)))
	return hex.EncodeToString(sum[:])[:8]
}

func lineErr(line int, message string) error {
	return fmt.Errorf("line %d: %s", line, message)
}
