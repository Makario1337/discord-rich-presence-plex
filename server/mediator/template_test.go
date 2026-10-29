package mediator

import (
	"drpp/server/plex"
	"testing"
)

func TestFormatGenres(t *testing.T) {
	tests := []struct {
		name   string
		genres any
		want   string
	}{
		{"strings", []string{"Rock", " Pop ", "Jazz", "Blues"}, "Rock, Pop, Jazz"},
		{"plex", []plex.Genre{{Tag: "Drama"}, {Tag: "Comedy"}}, "Drama, Comedy"},
		{"jellyfin raw", []any{"Drama", "Comedy"}, "Drama, Comedy"},
		{"subsonic raw", []any{map[string]any{"name": "Rock"}, map[string]any{"name": "Pop"}}, "Rock, Pop"},
		{"nil", nil, ""},
		{"not a slice", "Rock", ""},
	}
	for _, test := range tests {
		if got := formatGenres(test.genres, ", ", 3); got != test.want {
			t.Errorf("%s: got %q, want %q", test.name, got, test.want)
		}
	}
}
