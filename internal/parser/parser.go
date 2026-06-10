package parser

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/robfig/cron/v3"
)

type Job struct {
	ID       string
	Line     int
	Schedule string
	CWD      string
	Prompt   string
	Raw      string
}

type ParseErrors []error

func (errs ParseErrors) Error() string {
	lines := make([]string, 0, len(errs))
	for _, err := range errs {
		lines = append(lines, err.Error())
	}
	return strings.Join(lines, "\n")
}

func ParseFile(content string) ([]Job, error) {
	var jobs []Job
	var errs ParseErrors

	lines := strings.Split(content, "\n")
	for index, raw := range lines {
		lineNumber := index + 1
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		job, err := parseLine(lineNumber, trimmed)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		jobs = append(jobs, job)
	}

	if len(errs) > 0 {
		return jobs, errs
	}
	return jobs, nil
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

func parseLine(lineNumber int, line string) (Job, error) {
	schedule, rest, err := splitSchedule(line)
	if err != nil {
		return Job{}, lineErr(lineNumber, err.Error())
	}

	if err := validateSchedule(schedule); err != nil {
		return Job{}, lineErr(lineNumber, err.Error())
	}

	cwd, prompt, err := parseCommand(rest)
	if err != nil {
		return Job{}, lineErr(lineNumber, err.Error())
	}

	expanded, err := expandCWD(cwd)
	if err != nil {
		return Job{}, lineErr(lineNumber, err.Error())
	}

	return Job{
		ID:       jobID(lineNumber, schedule, expanded, prompt),
		Line:     lineNumber,
		Schedule: schedule,
		CWD:      expanded,
		Prompt:   prompt,
		Raw:      line,
	}, nil
}

func splitSchedule(line string) (string, string, error) {
	if strings.HasPrefix(line, "@") {
		schedule, rest := readBare(line)
		if schedule == "" || strings.TrimSpace(rest) == "" {
			return "", "", errors.New("expected @schedule <cwd> \"<prompt>\"")
		}
		return schedule, rest, nil
	}

	rest := line
	parts := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		part, next := readBare(rest)
		if part == "" {
			return "", "", errors.New("expected five cron fields before cwd and prompt")
		}
		parts = append(parts, part)
		rest = next
	}
	if strings.TrimSpace(rest) == "" {
		return "", "", errors.New("expected <cron> <cwd> \"<prompt>\"")
	}
	return strings.Join(parts, " "), rest, nil
}

func parseCommand(input string) (string, string, error) {
	cwd, rest, _, err := readValue(input)
	if err != nil {
		return "", "", err
	}
	if cwd == "" {
		return "", "", errors.New("expected cwd after schedule")
	}

	prompt, rest, quoted, err := readValue(rest)
	if err != nil {
		return "", "", err
	}
	if prompt == "" {
		return "", "", errors.New("expected quoted prompt after cwd")
	}
	if !quoted {
		return "", "", errors.New("prompt must be quoted")
	}

	trailing := strings.TrimSpace(rest)
	if trailing != "" && !strings.HasPrefix(trailing, "#") {
		return "", "", fmt.Errorf("unexpected trailing text after prompt: %s", trailing)
	}

	return cwd, prompt, nil
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

func readValue(input string) (value string, rest string, quoted bool, err error) {
	input = strings.TrimLeftFunc(input, unicode.IsSpace)
	if input == "" {
		return "", "", false, nil
	}
	if input[0] != '"' {
		part, next := readBare(input)
		return part, next, false, nil
	}

	var out strings.Builder
	escaped := false
	for i := 1; i < len(input); i++ {
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
			return out.String(), input[i+1:], true, nil
		default:
			out.WriteByte(ch)
		}
	}
	return "", "", true, errors.New("unterminated quoted string")
}

func validateSchedule(spec string) error {
	if spec == "@reboot" {
		return errors.New("@reboot is not supported")
	}

	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	if _, err := parser.Parse(spec); err != nil {
		return fmt.Errorf("invalid schedule %q: %w", spec, err)
	}
	return nil
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
