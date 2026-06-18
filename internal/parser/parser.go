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
	"github.com/ryangerardwilson/looptab/internal/shellwords"
)

const DefaultTimezone = "UTC"

type File struct {
	Timezone string
	Location *time.Location
	Jobs     []Job
}

type JobKind string

const (
	JobKindCodex   JobKind = "codex"
	JobKindGrok    JobKind = "grok"
	JobKindCommand JobKind = "command"
)

type Job struct {
	ID        string
	Line      int
	Schedule  string
	CronSpecs []string
	Once      bool
	Timezone  string
	Kind      JobKind
	CWD       string
	Prompt    string
	Command   []string
	Raw       string
}

func (j Job) ActionDisplay() string {
	switch j.Kind {
	case JobKindCommand:
		return strings.Join(j.Command, " ")
	case JobKindGrok:
		return "@grok " + strconv.Quote(j.Prompt)
	default:
		return strconv.Quote(j.Prompt)
	}
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
	schedule, cwd, kind, prompt, command, err := splitJobLine(line)
	if err != nil {
		return Job{}, lineErr(lineNumber, err.Error())
	}

	cronSpecs, once, err := compileSchedule(schedule)
	if err != nil {
		return Job{}, lineErr(lineNumber, err.Error())
	}

	expanded, err := expandCWD(cwd)
	if err != nil {
		return Job{}, lineErr(lineNumber, err.Error())
	}

	return Job{
		ID:        jobID(lineNumber, schedule, expanded, kind, prompt, command),
		Line:      lineNumber,
		Schedule:  schedule,
		CronSpecs: cronSpecs,
		Once:      once,
		Timezone:  timezone,
		Kind:      kind,
		CWD:       expanded,
		Prompt:    prompt,
		Command:   command,
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

func splitJobLine(line string) (string, string, JobKind, string, []string, error) {
	tokens, err := scanTokens(line)
	if err != nil {
		return "", "", "", "", nil, err
	}
	if len(tokens) == 0 {
		return "", "", "", "", nil, errors.New("expected <when> [cwd] \"<prompt>\", @grok \"<prompt>\", or <executable> [args...]")
	}

	if promptIndex := quotedPromptIndex(tokens); promptIndex >= 0 {
		return splitAgentJobLine(line, tokens, promptIndex)
	}
	return splitCommandJobLine(line, tokens)
}

func splitAgentJobLine(line string, tokens []token, promptIndex int) (string, string, JobKind, string, []string, error) {
	promptToken := tokens[promptIndex]
	if promptToken.value == "" {
		return "", "", "", "", nil, errors.New("prompt must not be empty")
	}
	if err := ensureNoTrailingAction(line, promptToken.end); err != nil {
		return "", "", "", "", nil, err
	}

	kind := JobKindCodex
	scheduleEnd := promptToken.start
	cwd := "~"

	if promptIndex > 0 {
		previous := tokens[promptIndex-1]
		if agent, ok := agentKind(previous.value); ok {
			kind = agent
			scheduleEnd = previous.start
			if promptIndex > 1 {
				candidate := tokens[promptIndex-2]
				if isCWDToken(candidate.value) {
					cwd = candidate.value
					scheduleEnd = candidate.start
				} else if looksLikeRelativePath(candidate.value) {
					return "", "", "", "", nil, errors.New("cwd must be absolute or start with ~")
				}
			}
		} else if isCWDToken(previous.value) {
			cwd = previous.value
			scheduleEnd = previous.start
		} else if looksLikeRelativePath(previous.value) {
			return "", "", "", "", nil, errors.New("cwd must be absolute or start with ~")
		}
	}

	schedule := strings.TrimSpace(line[:scheduleEnd])
	if schedule == "" {
		return "", "", "", "", nil, errors.New("expected schedule before action")
	}
	return schedule, cwd, kind, promptToken.value, nil, nil
}

func splitCommandJobLine(line string, tokens []token) (string, string, JobKind, string, []string, error) {
	for _, tok := range tokens {
		if kind, ok := agentKind(tok.value); ok {
			return "", "", "", "", nil, fmt.Errorf("%s requires a quoted prompt", kindMarker(kind))
		}
	}

	scheduleCount, err := scheduleTokenCount(tokens)
	if err != nil {
		return "", "", "", "", nil, err
	}
	if scheduleCount >= len(tokens) {
		return "", "", "", "", nil, errors.New("expected an executable after the schedule")
	}

	schedule := strings.Join(tokenValues(tokens[:scheduleCount]), " ")
	remainder := tokens[scheduleCount:]
	cwd := "~"
	actionStart := 0

	if len(remainder) > 0 && looksLikeRelativePath(remainder[0].value) {
		return "", "", "", "", nil, errors.New("cwd must be absolute or start with ~")
	}
	if len(remainder) > 0 && isCWDToken(remainder[0].value) && remainderLooksLikeCWD(remainder) {
		cwd = remainder[0].value
		actionStart = 1
	}
	if actionStart >= len(remainder) {
		return "", "", "", "", nil, errors.New("expected an executable after the schedule")
	}

	action := remainder[actionStart:]
	if len(action) == 0 {
		return "", "", "", "", nil, errors.New("expected quoted prompt, @grok, @codex, or an executable path")
	}
	if kind, ok := agentKind(action[0].value); ok {
		return "", "", "", "", nil, fmt.Errorf("%s requires a quoted prompt", kindMarker(kind))
	}
	if !isExecutableToken(action[0].value) {
		return "", "", "", "", nil, errors.New("expected quoted prompt, @grok, @codex, or an executable path")
	}

	commandTail := strings.TrimSpace(line[action[0].start:])
	commentIndex := strings.Index(commandTail, " #")
	if commentIndex >= 0 {
		commandTail = strings.TrimSpace(commandTail[:commentIndex])
	}
	command, err := shellwords.Fields(commandTail)
	if err != nil {
		return "", "", "", "", nil, err
	}
	if len(command) == 0 {
		return "", "", "", "", nil, errors.New("executable path must not be empty")
	}
	return schedule, cwd, JobKindCommand, "", command, nil
}

func quotedPromptIndex(tokens []token) int {
	promptIndex := -1
	for i, tok := range tokens {
		if !tok.quoted && strings.HasPrefix(tok.value, "#") {
			break
		}
		if tok.quoted {
			promptIndex = i
		}
	}
	return promptIndex
}

func scheduleTokenCount(tokens []token) (int, error) {
	if len(tokens) < 2 {
		return 0, errors.New("expected schedule and action")
	}
	if len(tokens) == 3 &&
		strings.EqualFold(tokens[0].value, "hourly") &&
		strings.EqualFold(tokens[1].value, "at") {
		schedule := strings.Join(tokenValues(tokens), " ")
		if _, _, err := compileSchedule(schedule); err != nil {
			return 0, err
		}
	}

	var lastErr error
	for i := len(tokens) - 1; i >= 1; i-- {
		schedule := strings.Join(tokenValues(tokens[:i]), " ")
		_, _, err := compileSchedule(schedule)
		if err == nil {
			if strings.EqualFold(tokens[0].value, "hourly") &&
				strings.EqualFold(tokens[1].value, "at") &&
				i < 3 {
				lastErr = errors.New("hourly accepts no time or `at <minute>`")
				continue
			}
			return i, nil
		}
		lastErr = err
		if scheduleParseLooksComplete(err, i, len(tokens)) {
			return 0, err
		}
	}
	if lastErr != nil {
		return 0, lastErr
	}
	return 0, errors.New("expected schedule like `daily 11am`, `hourly`, or `now`")
}

func scheduleParseLooksComplete(err error, consumed, total int) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	if consumed < total && strings.Contains(text, "invalid time") {
		return false
	}
	switch {
	case strings.Contains(text, "now does not accept a time"):
		return true
	case strings.Contains(text, "invalid hourly minute"):
		return true
	case strings.Contains(text, "invalid time"):
		return true
	case strings.Contains(text, "expected at least one time"):
		return true
	default:
		return false
	}
}

func tokenValues(tokens []token) []string {
	values := make([]string, len(tokens))
	for i, tok := range tokens {
		values[i] = tok.value
	}
	return values
}

func agentKind(token string) (JobKind, bool) {
	switch strings.ToLower(token) {
	case "@codex":
		return JobKindCodex, true
	case "@grok":
		return JobKindGrok, true
	default:
		return "", false
	}
}

func kindMarker(kind JobKind) string {
	switch kind {
	case JobKindGrok:
		return "@grok"
	default:
		return "@codex"
	}
}

func ensureNoTrailingAction(line string, end int) error {
	trailing := strings.TrimSpace(line[end:])
	if trailing != "" && !strings.HasPrefix(trailing, "#") {
		return fmt.Errorf("unexpected trailing text after action: %s", trailing)
	}
	return nil
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

func looksLikeRelativePath(value string) bool {
	return value == "." ||
		value == ".." ||
		strings.HasPrefix(value, "./") ||
		strings.HasPrefix(value, "../")
}

func isExecutableToken(value string) bool {
	return isCWDToken(value)
}

func remainderLooksLikeCWD(remainder []token) bool {
	if len(remainder) < 2 {
		return false
	}
	next := remainder[1]
	if _, ok := agentKind(next.value); ok {
		return true
	}
	return next.quoted
}

func compileSchedule(input string) ([]string, bool, error) {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return nil, false, errors.New("expected schedule like `daily 11am`, `hourly`, or `now`")
	}

	frequency := strings.ToLower(parts[0])
	if frequency == "now" {
		if len(parts) > 1 {
			return nil, false, errors.New("now does not accept a time")
		}
		return nil, true, nil
	}
	if frequency == "hourly" || frequency == "hour" || frequency == "hours" {
		spec, err := compileHourlySpec(parts)
		if err != nil {
			return nil, false, err
		}
		return []string{spec}, false, nil
	}

	if len(parts) < 2 {
		return nil, false, errors.New("expected schedule like `daily 11am`, `hourly`, or `now`")
	}

	daySpec, err := compileDaySpec(frequency)
	if err != nil {
		return nil, false, err
	}

	timesText := strings.TrimSpace(strings.TrimPrefix(input, parts[0]))
	times, err := parseTimeList(timesText)
	if err != nil {
		return nil, false, err
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
			return nil, false, fmt.Errorf("compiled invalid schedule %q: %w", spec, err)
		}
		specs = append(specs, spec)
		seen[spec] = true
	}

	return specs, false, nil
}

func compileHourlySpec(parts []string) (string, error) {
	if len(parts) == 1 {
		return "0 * * * *", nil
	}
	if len(parts) != 3 || strings.ToLower(parts[1]) != "at" {
		return "", errors.New("hourly accepts no time or `at <minute>`")
	}

	minute, err := strconv.Atoi(parts[2])
	if err != nil || minute < 0 || minute > 59 {
		return "", fmt.Errorf("invalid hourly minute %q; use 0-59", parts[2])
	}
	return fmt.Sprintf("%d * * * *", minute), nil
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
		return "", fmt.Errorf("unknown schedule %q; use hourly, daily, weekdays, weekends, or a weekday name", frequency)
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

func jobID(line int, schedule, cwd string, kind JobKind, prompt string, command []string) string {
	var key string
	switch kind {
	case JobKindCommand:
		key = fmt.Sprintf("%d\x00%s\x00%s\x00command\x00%s", line, schedule, cwd, strings.Join(command, "\x00"))
	default:
		key = fmt.Sprintf("%d\x00%s\x00%s\x00%s\x00%s", line, schedule, cwd, kind, prompt)
	}
	sum := sha1.Sum([]byte(key))
	return hex.EncodeToString(sum[:])[:8]
}

func lineErr(line int, message string) error {
	return fmt.Errorf("line %d: %s", line, message)
}
