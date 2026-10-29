package jellyfin

import (
	"drpp/server/config"
	"testing"
)

func newTestService(serverConfig config.JellyfinServer) *Service {
	return NewService(serverConfig, func() {})
}

func fakeGetters(items map[string]*Item, libraryName string) (func(string, string, string) *Item, func(string, string) string) {
	getItem := func(itemId string, userId string, itemType string) *Item {
		return items[itemId]
	}
	getLibraryName := func(itemId string, userId string) string {
		return libraryName
	}
	return getItem, getLibraryName
}

func TestBuildActivityEpisode(t *testing.T) {
	episode := &Item{
		Id: "ep", Name: "Pilot", Type: "Episode", IndexNumber: 1, ParentIndexNumber: 2, RunTimeTicks: 30 * 60 * 1000 * ticksPerMs,
		SeriesId: "series", SeriesName: "Show", SeasonId: "season",
		ProviderIds: map[string]string{"Imdb": "tt1", "Tvdb": "123"},
	}
	items := map[string]*Item{
		"ep":     episode,
		"season": {Id: "season", Name: "Season 2", Type: "Season", IndexNumber: 2},
		"series": {Id: "series", Name: "Show", Type: "Series", ProductionYear: 2020, Genres: []string{"Drama"}, ImageTags: map[string]string{"Primary": "abc"}},
	}
	getItem, getLibraryName := fakeGetters(items, "TV")
	activity := newTestService(config.JellyfinServer{}).buildActivity(Session{NowPlayingItem: episode}, getItem, getLibraryName)
	if activity == nil {
		t.Fatal("expected activity")
	}
	if activity.MediaType != "episode" || activity.LibraryName != "TV" {
		t.Errorf("unexpected media type/library: %q/%q", activity.MediaType, activity.LibraryName)
	}
	if activity.Item.Title != "Pilot" || activity.Item.Index != 1 || activity.Item.DurationMs != 30*60*1000 {
		t.Errorf("unexpected item: %#v", activity.Item)
	}
	if activity.ParentItem.Index != 2 {
		t.Errorf("unexpected season number: %d", activity.ParentItem.Index)
	}
	if activity.GrandparentItem.Title != "Show" || activity.GrandparentItem.Year != 2020 || activity.GrandparentItem.Thumb != "/Items/series/Images/Primary?tag=abc" {
		t.Errorf("unexpected series: %#v", activity.GrandparentItem)
	}
	if activity.Item.ProviderIds["imdb"] != "tt1" || activity.Item.ProviderIds["tvdb"] != "123" {
		t.Errorf("unexpected provider IDs: %#v", activity.Item.ProviderIds)
	}
}

func TestBuildActivityTrack(t *testing.T) {
	track := &Item{
		Id: "track", Name: "Song", Type: "Audio", Artists: []string{"A", "B"},
		AlbumId: "album", Album: "Record", AlbumArtists: []NameIdPair{{Name: "A", Id: "artist"}},
		ProviderIds: map[string]string{"MusicBrainzTrack": "t-mbid", "MusicBrainzAlbum": "al-mbid", "MusicBrainzArtist": "ar-mbid"},
	}
	items := map[string]*Item{
		"track":  track,
		"album":  {Id: "album", Name: "Record", Type: "MusicAlbum", ProductionYear: 1999, ProviderIds: map[string]string{"MusicBrainzAlbum": "al-mbid", "MusicBrainzArtist": "ar-mbid"}},
		"artist": {Id: "artist", Name: "A", Type: "MusicArtist", ProviderIds: map[string]string{"MusicBrainzArtist": "ar-mbid"}},
	}
	getItem, getLibraryName := fakeGetters(items, "Music")
	activity := newTestService(config.JellyfinServer{}).buildActivity(Session{NowPlayingItem: track}, getItem, getLibraryName)
	if activity == nil {
		t.Fatal("expected activity")
	}
	if activity.MediaType != "track" || activity.Item.Artist != "A, B" {
		t.Errorf("unexpected track: %q %#v", activity.MediaType, activity.Item)
	}
	if activity.ParentItem.Title != "Record" || activity.ParentItem.Year != 1999 || activity.GrandparentItem.Title != "A" {
		t.Errorf("unexpected album/artist: %#v %#v", activity.ParentItem, activity.GrandparentItem)
	}
	if activity.Item.ProviderIds["mbid"] != "t-mbid" || activity.ParentItem.ProviderIds["mbid"] != "al-mbid" || activity.GrandparentItem.ProviderIds["mbid"] != "ar-mbid" {
		t.Errorf("unexpected MusicBrainz IDs: %v %v %v", activity.Item.ProviderIds, activity.ParentItem.ProviderIds, activity.GrandparentItem.ProviderIds)
	}
}

func TestBuildActivityLibraryFilters(t *testing.T) {
	movie := &Item{Id: "movie", Name: "Film", Type: "Movie"}
	getItem, getLibraryName := fakeGetters(map[string]*Item{"movie": movie}, "Kids")
	session := Session{NowPlayingItem: movie}
	if newTestService(config.JellyfinServer{BlacklistedLibraries: []string{"kids"}}).buildActivity(session, getItem, getLibraryName) != nil {
		t.Error("expected blacklisted library to be ignored")
	}
	if newTestService(config.JellyfinServer{WhitelistedLibraries: []string{"Movies"}}).buildActivity(session, getItem, getLibraryName) != nil {
		t.Error("expected non-whitelisted library to be ignored")
	}
	if newTestService(config.JellyfinServer{WhitelistedLibraries: []string{"KIDS"}}).buildActivity(session, getItem, getLibraryName) == nil {
		t.Error("expected whitelisted library to be processed")
	}
}
