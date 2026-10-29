package subsonic

import (
	"crypto/md5" //nolint:gosec
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var testResponses = map[string]string{
	"ping": `{"subsonic-response": {"status": "ok", "version": "1.16.1", "type": "navidrome", "serverVersion": "0.58.0", "openSubsonic": true}}`,
	"getNowPlaying": `{"subsonic-response": {"status": "ok", "nowPlaying": {"entry": [
		{"id": "s1", "title": "Other", "username": "bob", "playerId": 1, "duration": 100},
		{"id": "s2", "title": "Song", "album": "Record", "albumId": "al1", "artist": "A feat. B", "artistId": "ar1",
		 "artists": [{"id": "ar1", "name": "A"}, {"id": "ar2", "name": "B"}], "albumArtists": [{"id": "ar1", "name": "A"}],
		 "coverArt": "mf-s2", "duration": 200, "year": 1999, "track": 3, "genres": [{"name": "Rock"}], "musicBrainzId": "t-mbid",
		 "username": "Alice", "minutesAgo": 0, "playerId": 7, "playerName": "Web", "state": "paused", "positionMs": 42000, "playbackRate": 1.0}
	]}}}`,
	"getAlbum":  `{"subsonic-response": {"status": "ok", "album": {"id": "al1", "name": "Record", "artist": "A", "artistId": "ar1", "coverArt": "al-al1", "year": 1999, "genre": "Rock", "musicBrainzId": "al-mbid", "song": [{"id": "s2"}]}}}`,
	"getArtist": `{"subsonic-response": {"status": "ok", "artist": {"id": "ar1", "name": "A", "coverArt": "ar-ar1", "musicBrainzId": "ar-mbid", "album": []}}}`,
}

func newTestServer(password string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		hash := md5.Sum([]byte(password + query.Get("s"))) //nolint:gosec
		if query.Get("u") != "alice" || query.Get("t") != hex.EncodeToString(hash[:]) || query.Get("f") != "json" {
			_, _ = w.Write([]byte(`{"subsonic-response": {"status": "failed", "error": {"code": 40, "message": "Wrong username or password"}}}`))
			return
		}
		response, ok := testResponses[strings.TrimPrefix(r.URL.Path, "/rest/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(response))
	}))
}

func TestClientAndBuildActivity(t *testing.T) {
	server := newTestServer("secret")
	defer server.Close()
	ctx := t.Context()

	client := NewClient(server.URL+"/", "alice", "secret", "", 5*time.Second)
	info, err := client.Ping(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Type != "navidrome" || !info.OpenSubsonic {
		t.Errorf("unexpected server info: %#v", info)
	}

	entries, err := client.GetNowPlaying(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].PlayerId != "1" || entries[1].PositionMs == nil || *entries[1].PositionMs != 42000 || entries[1].State != "paused" {
		t.Fatalf("unexpected entries: %#v", entries)
	}

	activity := buildActivity(&entries[1], func(id string) *Album {
		album, err := client.GetAlbum(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return album
	}, func(id string) *Artist {
		artist, err := client.GetArtist(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return artist
	})
	if activity.Item.Title != "Song" || activity.Item.Artist != "A, B" || activity.Item.DurationMs != 200000 || activity.Item.Thumb != "/rest/getCoverArt?id=mf-s2" {
		t.Errorf("unexpected item: %#v", activity.Item)
	}
	if activity.ParentItem.Title != "Record" || activity.ParentItem.Thumb != "/rest/getCoverArt?id=al-al1" || activity.ParentItem.ProviderIds["mbid"] != "al-mbid" {
		t.Errorf("unexpected album: %#v", activity.ParentItem)
	}
	if activity.GrandparentItem.Title != "A" || activity.GrandparentItem.ProviderIds["mbid"] != "ar-mbid" {
		t.Errorf("unexpected artist: %#v", activity.GrandparentItem)
	}
	if raw, ok := activity.RawParentItem.(map[string]any); !ok || raw["song"] != nil {
		t.Errorf("expected raw album without songs: %#v", activity.RawParentItem)
	}

	thumbUrl, _ := client.GetThumbUrl(activity.ParentItem.Thumb)
	if !strings.Contains(thumbUrl, "/rest/getCoverArt?") || !strings.Contains(thumbUrl, "id=al-al1") || !strings.Contains(thumbUrl, "u=alice") {
		t.Errorf("unexpected thumb URL: %s", thumbUrl)
	}

	wrongClient := NewClient(server.URL, "alice", "wrong", "", 5*time.Second)
	if _, err := wrongClient.Ping(ctx); err == nil || !strings.Contains(err.Error(), "40") {
		t.Errorf("expected Subsonic error 40, got %v", err)
	}
}
