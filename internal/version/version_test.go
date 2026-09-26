package version

import "testing"

func TestBuildStringWithAndWithoutCommit(t *testing.T) {
	oldVersion, oldCommit := Version, GitCommit
	t.Cleanup(func() { Version, GitCommit = oldVersion, oldCommit })
	Version = "2026.09.26.1"
	for _, tc := range []struct{ commit, want string }{
		{"b36fa42", "v2026.09.26.1-b36fa42"},
		{"", "v2026.09.26.1"},
		{Unknown, "v2026.09.26.1"},
	} {
		GitCommit = tc.commit
		if got := BuildString(); got != tc.want {
			t.Errorf("commit %q: got %q, want %q", tc.commit, got, tc.want)
		}
	}
}
