package media

const (
	SourcePlex     = "plex"
	SourceJellyfin = "jellyfin"
)

// Activity is a media server agnostic representation of a playback session
type Activity struct {
	Source            string   `json:"source"`
	MediaType         string   `json:"mediaType"`
	State             string   `json:"state"`
	ElapsedDurationMs int64    `json:"elapsedDurationMs"`
	LibraryName       string   `json:"libraryName,omitzero"`
	Item              Metadata `json:"item"`
	ParentItem        Metadata `json:"parentItem,omitzero"`
	GrandparentItem   Metadata `json:"grandparentItem,omitzero"`
	// Raw item objects as returned by the media server, exposed to templates
	RawItem            any                                            `json:"-"`
	RawParentItem      any                                            `json:"-"`
	RawGrandparentItem any                                            `json:"-"`
	GetThumbUrl        func(thumb string) (string, map[string]string) `json:"-"`
}

type Metadata struct {
	Title      string   `json:"title,omitzero"`
	Artist     string   `json:"artist,omitzero"` // Track artist, for tracks
	Year       int64    `json:"year,omitzero"`
	Index      int64    `json:"index,omitzero"`
	Thumb      string   `json:"thumb,omitzero"`
	DurationMs int64    `json:"durationMs,omitzero"`
	Genres     []string `json:"genres,omitempty"`
	// Keys: imdb, tmdb, tvdb, mbid
	ProviderIds map[string]string `json:"providerIds,omitempty"`
}
