package fetch

import (
	"errors"
	"testing"
)

// FetchError's three shapes, exactly. It does not name its repository: the
// one place it is printed, cmd/'s failureMessage, already has.
func TestFetchErrorNamesWhatFailed(t *testing.T) {
	boom := errors.New("boom")
	for _, tt := range []struct {
		name string
		err  *FetchError
		want string
	}{
		{"no paths", &FetchError{Err: boom}, "cannot fetch files: boom"},
		{"one path", &FetchError{Paths: []string{"docs/index.md"}, Err: boom}, "cannot fetch docs/index.md: boom"},
		{"several paths", &FetchError{Paths: []string{"a.md", "b.md", "c.md"}, Err: boom},
			"cannot fetch 3 files, starting with a.md: boom"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}
