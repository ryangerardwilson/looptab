package parser

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/ryangerardwilson/looptab/internal/shellwords"
)

type Step struct {
	Kind      JobKind
	Prompt    string
	Command   []string
	OnSuccess *Step
	OnFailure *Step
}

func (s Step) Display() string {
	base := s.displayPrimary()
	if s.OnSuccess == nil && s.OnFailure == nil {
		return base
	}
	text := base + " ? " + s.OnSuccess.Display()
	if s.OnFailure != nil {
		text += " : " + s.OnFailure.Display()
	}
	return text
}

func (s Step) displayPrimary() string {
	switch s.Kind {
	case JobKindCommand:
		return strings.Join(s.Command, " ")
	case JobKindGrok:
		return "@grok " + strconvQuote(s.Prompt)
	default:
		return strconvQuote(s.Prompt)
	}
}

func strconvQuote(value string) string {
	return fmt.Sprintf("%q", value)
}

func splitJobLine(line string) (string, string, []Step, error) {
	tokens, err := scanTokens(line)
	if err != nil {
		return "", "", nil, err
	}
	if len(tokens) == 0 {
		return "", "", nil, errors.New("expected <when> [cwd] <action> [? on-success [: on-failure]] [&& ...]")
	}

	scheduleCount, err := parseSchedulePrefix(tokens)
	if err != nil {
		return "", "", nil, err
	}
	if scheduleCount >= len(tokens) {
		return "", "", nil, errors.New("expected an action after the schedule")
	}

	schedule := strings.Join(tokenValues(tokens[:scheduleCount]), " ")
	remainder := tokens[scheduleCount:]
	cwd := "~"
	actionIndex := 0

	if len(remainder) > 0 && looksLikeRelativePath(remainder[0].value) {
		return "", "", nil, errors.New("cwd must be absolute or start with ~")
	}
	if len(remainder) > 0 && isCWDToken(remainder[0].value) && remainderLooksLikeCWD(remainder) {
		cwd = remainder[0].value
		actionIndex = 1
	}
	if actionIndex >= len(remainder) {
		return "", "", nil, errors.New("expected an action after the schedule")
	}

	actionText := strings.TrimSpace(stripLineComment(line[remainder[actionIndex].start:]))
	if actionText == "" {
		return "", "", nil, errors.New("expected an action after the schedule")
	}

	steps, err := parseActionChain(actionText)
	if err != nil {
		return "", "", nil, err
	}
	return schedule, cwd, steps, nil
}

func parseSchedulePrefix(tokens []token) (int, error) {
	if len(tokens) == 0 {
		return 0, errors.New("expected schedule like `daily 11am`, `hourly`, `every 30s`, or `now`")
	}

	frequency := strings.ToLower(tokens[0].value)
	switch frequency {
	case "every":
		if len(tokens) < 2 {
			return 0, errors.New("expected every <duration>")
		}
		if _, err := parseEveryDuration(tokens[1].value); err != nil {
			return 0, err
		}
		return 2, nil
	case "now":
		if len(tokens) >= 2 {
			if _, err := parseClock(tokens[1].value); err == nil {
				return 0, errors.New("now does not accept a time")
			}
		}
		return 1, nil
	case "hourly":
		if len(tokens) >= 3 && strings.EqualFold(tokens[1].value, "at") {
			if _, err := compileHourlySpec(tokenValues(tokens[:3])); err != nil {
				return 0, err
			}
			return 3, nil
		}
		return 1, nil
	default:
		if len(tokens) < 2 {
			return 0, errors.New("expected schedule like `daily 11am`, `hourly`, or `now`")
		}
		if _, err := compileDaySpec(frequency); err != nil {
			return 0, err
		}
		if _, err := parseTimeList(tokens[1].value); err != nil {
			return 0, err
		}
		return 2, nil
	}
}

func parseActionChain(actionText string) ([]Step, error) {
	segments := splitOutsideQuotes(actionText, "&&")
	if len(segments) == 0 {
		return nil, errors.New("expected an action")
	}

	steps := make([]Step, 0, len(segments))
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			return nil, errors.New("empty action step in chain")
		}
		step, err := parseActionSegment(segment)
		if err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	return steps, nil
}

func parseActionSegment(segment string) (Step, error) {
	primary, successText, failureText, hasOutcome, err := splitOutcome(segment)
	if err != nil {
		return Step{}, err
	}

	step, err := parsePrimaryAction(primary)
	if err != nil {
		return Step{}, err
	}
	if !hasOutcome {
		return step, nil
	}

	onSuccess, err := parseOutcomeBranch(successText, false)
	if err != nil {
		return Step{}, fmt.Errorf("success outcome: %w", err)
	}
	step.OnSuccess = &onSuccess
	if failureText != "" {
		onFailure, err := parseOutcomeBranch(failureText, true)
		if err != nil {
			return Step{}, fmt.Errorf("failure outcome: %w", err)
		}
		step.OnFailure = &onFailure
	}
	return step, nil
}

func parsePrimaryAction(segment string) (Step, error) {
	tokens, err := scanTokens(segment)
	if err != nil {
		return Step{}, err
	}
	if len(tokens) == 0 {
		return Step{}, errors.New("empty action step")
	}

	if kind, ok := agentKind(tokens[0].value); ok {
		if len(tokens) < 2 || !tokens[1].quoted {
			return Step{}, fmt.Errorf("%s requires a quoted prompt", kindMarker(kind))
		}
		if tokens[1].value == "" {
			return Step{}, errors.New("prompt must not be empty")
		}
		if len(tokens) > 2 {
			return Step{}, errors.New("unexpected text after agent prompt")
		}
		return Step{Kind: kind, Prompt: tokens[1].value}, nil
	}

	if tokens[0].quoted {
		if tokens[0].value == "" {
			return Step{}, errors.New("prompt must not be empty")
		}
		if len(tokens) > 1 {
			return Step{}, errors.New("unexpected text after prompt")
		}
		return Step{Kind: JobKindCodex, Prompt: tokens[0].value}, nil
	}

	if !isCommandToken(tokens[0].value) {
		return Step{}, errors.New("expected quoted prompt, @grok, @codex, or a command")
	}

	command, err := shellwords.Fields(segment)
	if err != nil {
		return Step{}, err
	}
	if len(command) == 0 {
		return Step{}, errors.New("command must not be empty")
	}
	return Step{Kind: JobKindCommand, Command: command}, nil
}

func splitOutcome(segment string) (primary, success, failure string, hasOutcome bool, err error) {
	qIdx := findOutsideQuotes(segment, "?")
	if qIdx < 0 {
		return segment, "", "", false, nil
	}

	primary = strings.TrimSpace(segment[:qIdx])
	if primary == "" {
		return "", "", "", false, errors.New("expected action before ?")
	}

	rest := strings.TrimSpace(segment[qIdx+1:])
	if rest == "" {
		return "", "", "", false, errors.New("expected outcome after ?")
	}

	colonIdx := findOutsideQuotes(rest, ":")
	if colonIdx < 0 {
		return primary, rest, "", true, nil
	}

	success = strings.TrimSpace(rest[:colonIdx])
	failure = strings.TrimSpace(rest[colonIdx+1:])
	if success == "" {
		return "", "", "", false, errors.New("expected success outcome after ?")
	}
	if failure == "" {
		return "", "", "", false, errors.New("expected failure outcome after :")
	}
	return primary, success, failure, true, nil
}

func parseOutcomeBranch(text string, isFailure bool) (Step, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Step{}, errors.New("outcome branch must not be empty")
	}

	tokens, err := scanTokens(text)
	if err != nil {
		return Step{}, err
	}
	if len(tokens) == 1 && tokens[0].quoted {
		command := []string{"notify", tokens[0].value}
		if isFailure {
			command = append(command, "--urgency", "critical")
		}
		return Step{Kind: JobKindCommand, Command: command}, nil
	}

	return parsePrimaryAction(text)
}

func findOutsideQuotes(input, marker string) int {
	inQuote := false
	escaped := false
	for i := 0; i < len(input); i++ {
		ch := input[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && inQuote {
			escaped = true
			continue
		}
		if ch == '"' {
			inQuote = !inQuote
			continue
		}
		if !inQuote && strings.HasPrefix(input[i:], marker) {
			return i
		}
	}
	return -1
}

func splitOutsideQuotes(input, separator string) []string {
	var segments []string
	var current strings.Builder
	inQuote := false
	escaped := false

	for i := 0; i < len(input); i++ {
		ch := input[i]
		if escaped {
			current.WriteByte(ch)
			escaped = false
			continue
		}
		if ch == '\\' && inQuote {
			escaped = true
			current.WriteByte(ch)
			continue
		}
		if ch == '"' {
			inQuote = !inQuote
			current.WriteByte(ch)
			continue
		}
		if !inQuote && strings.HasPrefix(input[i:], separator) {
			segments = append(segments, current.String())
			current.Reset()
			i += len(separator) - 1
			continue
		}
		current.WriteByte(ch)
	}
	segments = append(segments, current.String())
	return segments
}

func stripLineComment(input string) string {
	inQuote := false
	escaped := false
	for i := 0; i < len(input); i++ {
		ch := input[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && inQuote {
			escaped = true
			continue
		}
		if ch == '"' {
			inQuote = !inQuote
			continue
		}
		if !inQuote && ch == '#' && (i == 0 || unicode.IsSpace(rune(input[i-1]))) {
			return strings.TrimSpace(input[:i])
		}
	}
	return strings.TrimSpace(input)
}

func isCommandToken(value string) bool {
	if value == "" || strings.HasPrefix(value, "@") {
		return false
	}
	if isCWDToken(value) {
		return true
	}
	return !looksLikeRelativePath(value)
}

func FlattenSteps(steps []Step) []Step {
	if len(steps) == 0 {
		return nil
	}
	flat := make([]Step, 0, len(steps))
	for _, step := range steps {
		flat = append(flat, Step{
			Kind:    step.Kind,
			Prompt:  step.Prompt,
			Command: step.Command,
		})
		if step.OnSuccess != nil {
			flat = append(flat, *step.OnSuccess)
		}
		if step.OnFailure != nil {
			flat = append(flat, *step.OnFailure)
		}
	}
	return flat
}

func syncPrimaryFields(job *Job) {
	if len(job.Steps) == 0 {
		return
	}
	first := job.Steps[0]
	job.Kind = first.Kind
	job.Prompt = first.Prompt
	job.Command = first.Command
}

func (j Job) ActionDisplay() string {
	if len(j.Steps) == 0 {
		switch j.Kind {
		case JobKindCommand:
			return strings.Join(j.Command, " ")
		case JobKindGrok:
			return "@grok " + strconvQuote(j.Prompt)
		default:
			return strconvQuote(j.Prompt)
		}
	}
	parts := make([]string, len(j.Steps))
	for i, step := range j.Steps {
		parts[i] = step.Display()
	}
	return strings.Join(parts, " && ")
}