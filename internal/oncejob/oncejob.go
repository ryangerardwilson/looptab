package oncejob

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/ryangerardwilson/looptab/internal/parser"
	"github.com/ryangerardwilson/looptab/internal/paths"
)

type Store struct {
	paths paths.Paths
	path  string
}

type State struct {
	Jobs map[string]Claim `json:"jobs"`
}

type Claim struct {
	JobID     string    `json:"job_id"`
	Line      int       `json:"line"`
	Schedule  string    `json:"schedule"`
	CWD       string    `json:"cwd"`
	Prompt    string    `json:"prompt"`
	ClaimedAt time.Time `json:"claimed_at"`
}

func NewStore(p paths.Paths) Store {
	return Store{
		paths: p,
		path:  filepath.Join(p.StateDir, "now-runs.json"),
	}
}

func (s Store) Claim(job parser.Job) (bool, error) {
	if err := paths.EnsureState(s.paths); err != nil {
		return false, err
	}
	state, err := s.read()
	if err != nil {
		return false, err
	}
	if _, ok := state.Jobs[job.ID]; ok {
		return false, nil
	}
	state.Jobs[job.ID] = Claim{
		JobID:     job.ID,
		Line:      job.Line,
		Schedule:  job.Schedule,
		CWD:       job.CWD,
		Prompt:    job.Prompt,
		ClaimedAt: time.Now(),
	}
	if err := s.write(state); err != nil {
		return false, err
	}
	return true, nil
}

func (s Store) read() (State, error) {
	content, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{Jobs: map[string]Claim{}}, nil
		}
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(content, &state); err != nil {
		return State{}, err
	}
	if state.Jobs == nil {
		state.Jobs = map[string]Claim{}
	}
	return state, nil
}

func (s Store) write(state State) error {
	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(content, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
