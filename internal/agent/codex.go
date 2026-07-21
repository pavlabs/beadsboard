package agent

// codexBackend is a placeholder for the Codex CLI backend so the Manager's
// backend map compiles today. Its methods borrow claude's shapes as stand-ins.
//
// TODO: implement against Codex's real CLI flags and output format — this is a
// stub, wiring only; parsing Codex output is a separate task.
type codexBackend struct{ bin string }

func (c codexBackend) Bin() string { return c.bin }

func (c codexBackend) HeadlessArgs(spec Spec) []string {
	return claudeBackend{}.HeadlessArgs(spec)
}

func (c codexBackend) Parse(line []byte) (Event, bool) {
	return claudeBackend{}.Parse(line)
}

func (c codexBackend) ResumeArgs(session string) []string {
	return []string{"--resume", session}
}

func (c codexBackend) InteractiveArgs(prompt string) []string {
	return []string{prompt}
}
