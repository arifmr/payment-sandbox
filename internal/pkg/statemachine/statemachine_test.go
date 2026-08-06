package statemachine

import "testing"

type status string

const (
	pending  status = "PENDING"
	approved status = "APPROVED"
	rejected status = "REJECTED"
	done     status = "DONE"
)

func newTestFSM() *FSM[status] {
	return New(map[status][]status{
		pending:  {approved, rejected},
		approved: {done},
	})
}

func TestFSM_Can(t *testing.T) {
	fsm := newTestFSM()

	cases := []struct {
		name     string
		from, to status
		want     bool
	}{
		{"allowed first hop", pending, approved, true},
		{"allowed sibling hop", pending, rejected, true},
		{"allowed second hop", approved, done, true},
		{"skipping a state", pending, done, false},
		{"backwards", approved, pending, false},
		{"self transition", pending, pending, false},
		{"from terminal state", done, approved, false},
		{"from unknown state", status("GONE"), approved, false},
		{"to unknown state", pending, status("GONE"), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fsm.Can(tc.from, tc.to); got != tc.want {
				t.Errorf("Can(%s, %s) = %v, want %v", tc.from, tc.to, got, tc.want)
			}
		})
	}
}

func TestFSM_EmptyGraphRejectsEverything(t *testing.T) {
	fsm := New(map[status][]status{})
	if fsm.Can(pending, approved) {
		t.Error("empty FSM must reject every transition")
	}
}

// A state declared with no outgoing edges is terminal, not a wildcard.
func TestFSM_TerminalStateWithEmptySlice(t *testing.T) {
	fsm := New(map[status][]status{done: {}})
	if fsm.Can(done, pending) {
		t.Error("state with empty transition list must be terminal")
	}
}
