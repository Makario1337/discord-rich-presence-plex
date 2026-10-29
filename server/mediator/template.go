package mediator

import (
	"bytes"
	"drpp/server/logger"
	"drpp/server/media"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"text/template"
	"time"
	"unicode"
)

func buildTemplateData(activity *media.Activity) map[string]any {
	data := make(map[string]any)
	data["Source"] = activity.Source
	data["MediaType"] = activity.MediaType
	data["State"] = activity.State
	data["LibraryName"] = strings.TrimSpace(activity.LibraryName)
	data["ElapsedDurationMs"] = activity.ElapsedDurationMs
	addProviderUrls := func(providerIds map[string]string, prefix string) {
		if id, ok := providerIds["imdb"]; ok {
			data[prefix+"ImdbUrl"] = "https://www.imdb.com/title/" + id
		}
		if id, ok := providerIds["tmdb"]; ok {
			var tmdbPathSegment, traktIdType string
			if activity.MediaType == "movie" { //nolint:gocritic
				tmdbPathSegment = "movie"
				traktIdType = "movie"
			} else if prefix == "Episode" {
				tmdbPathSegment = "episode"
				traktIdType = "episode"
			} else {
				tmdbPathSegment = "tv"
				traktIdType = "show"
			}
			data[prefix+"TmdbUrl"] = "https://www.themoviedb.org/" + tmdbPathSegment + "/" + id
			data[prefix+"TraktUrl"] = "https://trakt.tv/search/tmdb/" + id + "?id_type=" + traktIdType
			if activity.MediaType == "movie" {
				data[prefix+"LetterboxdUrl"] = "https://letterboxd.com/tmdb/" + id
			}
		}
		if id, ok := providerIds["tvdb"]; ok {
			var tvdbPathSegment string
			if activity.MediaType == "movie" { //nolint:gocritic
				tvdbPathSegment = "movie"
			} else if prefix == "Episode" {
				tvdbPathSegment = "episode"
			} else {
				tvdbPathSegment = "series"
			}
			data[prefix+"TvdbUrl"] = "https://www.thetvdb.com/dereferrer/" + tvdbPathSegment + "/" + id
		}
		if id, ok := providerIds["mbid"]; ok {
			data[prefix+"MusicBrainzUrl"] = "https://musicbrainz.org/" + strings.ToLower(prefix) + "/" + id
		}
	}
	switch activity.MediaType {
	case "movie":
		data["Title"] = stripYearSuffix(strings.TrimSpace(activity.Item.Title))
		data["Year"] = activity.Item.Year
		data["Duration"] = formatDuration(activity.Item.DurationMs, "")
		data["Genres"] = formatGenres(activity.Item.Genres, ", ", 3)
		data["Poster"] = activity.Item.Thumb
		addProviderUrls(activity.Item.ProviderIds, "")
	case "episode":
		data["ShowTitle"] = stripYearSuffix(strings.TrimSpace(activity.GrandparentItem.Title))
		data["ShowYear"] = activity.GrandparentItem.Year
		data["EpisodeDuration"] = formatDuration(activity.Item.DurationMs, "")
		data["ShowGenres"] = formatGenres(activity.GrandparentItem.Genres, ", ", 3)
		data["ShowPoster"] = activity.GrandparentItem.Thumb
		data["SeasonNumber"] = activity.ParentItem.Index
		data["EpisodeNumber"] = activity.Item.Index
		data["EpisodeTitle"] = strings.TrimSpace(activity.Item.Title)
		addProviderUrls(activity.Item.ProviderIds, "Episode")
		addProviderUrls(activity.GrandparentItem.ProviderIds, "Show")
	case "track":
		data["Title"] = strings.TrimSpace(activity.Item.Title)
		artist := strings.TrimSpace(activity.Item.Artist)
		if artist != "" {
			data["Artist"] = artist
		} else {
			data["Artist"] = strings.TrimSpace(activity.GrandparentItem.Title)
		}
		data["Album"] = stripYearSuffix(strings.TrimSpace(activity.ParentItem.Title))
		data["Year"] = activity.ParentItem.Year
		data["AlbumArtist"] = strings.TrimSpace(activity.GrandparentItem.Title)
		data["AlbumPoster"] = activity.ParentItem.Thumb
		data["ArtistPoster"] = activity.GrandparentItem.Thumb
		data["Duration"] = formatDuration(activity.Item.DurationMs, "")
		data["AlbumGenres"] = formatGenres(activity.ParentItem.Genres, ", ", 3)
		data["ArtistGenres"] = formatGenres(activity.GrandparentItem.Genres, ", ", 3)
		addProviderUrls(activity.Item.ProviderIds, "Track")
		addProviderUrls(activity.ParentItem.ProviderIds, "Album")
		addProviderUrls(activity.GrandparentItem.ProviderIds, "Artist")
	case "clip":
		data["Title"] = strings.TrimSpace(activity.Item.Title)
		data["Duration"] = formatDuration(activity.Item.DurationMs, "")
		data["Poster"] = activity.Item.Thumb
	case "liveEpisode":
		data["ShowTitle"] = strings.TrimSpace(activity.GrandparentItem.Title)
		data["EpisodeTitle"] = strings.TrimSpace(activity.Item.Title)
		data["ShowPoster"] = activity.GrandparentItem.Thumb
	}
	data["Item"] = activity.RawItem
	data["ParentItem"] = activity.RawParentItem
	data["GrandparentItem"] = activity.RawGrandparentItem
	return data
}

var templateCache sync.Map // map[string]*template.Template

var templateFuncs = template.FuncMap{
	"formatDuration":  formatDuration,
	"formatGenres":    formatGenres,
	"toSentenceCase":  toSentenceCase,
	"adjustLength":    adjustLength,
	"stripNonAscii":   stripNonAscii,
	"stripYearSuffix": stripYearSuffix,
}

func renderTemplate(tmpl string, data map[string]any) string {
	var t *template.Template
	if val, ok := templateCache.Load(tmpl); ok {
		t = val.(*template.Template) //nolint:errcheck,forcetypeassert
	} else {
		var err error
		t, err = template.New("").Funcs(templateFuncs).Parse(tmpl)
		if err != nil {
			logger.Error(err, "Failed to parse template %q", tmpl)
			return tmpl
		}
		templateCache.Store(tmpl, t)
	}
	var buffer bytes.Buffer
	if err := t.Execute(&buffer, data); err != nil {
		logger.Error(err, "Failed to execute template %q", tmpl)
		return tmpl
	}
	return strings.ReplaceAll(buffer.String(), "<no value>", "")
}

func formatDuration(milliseconds int64, format string) string {
	duration := time.Duration(milliseconds) * time.Millisecond
	if format == "" {
		return duration.Round(time.Second).String()
	}
	hours := int(duration.Hours())
	minutes := int(duration.Minutes()) % 60
	seconds := int(duration.Seconds()) % 60
	return fmt.Sprintf(format, hours, minutes, seconds)
}

// Accepts a slice of strings, fmt.Stringer values or objects with a name, so that it works with genres from the normalised metadata as well as raw Plex/Jellyfin/Subsonic items
func formatGenres(genres any, delimiter string, maxGenres int) string {
	value := reflect.ValueOf(genres)
	if value.Kind() != reflect.Slice {
		return ""
	}
	var genreNames []string
	for i := range value.Len() {
		var name string
		switch genre := value.Index(i).Interface().(type) {
		case string:
			name = genre
		case fmt.Stringer:
			name = genre.String()
		case map[string]any: // e.g. raw Subsonic genres
			name, _ = genre["name"].(string)
		default:
			continue
		}
		genreNames = append(genreNames, strings.TrimSpace(name))
		if len(genreNames) == maxGenres {
			break
		}
	}
	return strings.Join(genreNames, delimiter)
}

func toSentenceCase(text string) string {
	if text == "" {
		return text
	}
	chars := []rune(text)
	chars[0] = unicode.ToUpper(chars[0])
	return string(chars)
}

func adjustLength(text string, maxLength int, ellipsis string, minLength int, padding string) string {
	if maxLength < 0 {
		maxLength = 0
	}
	runes := []rune(text)
	length := len(runes)
	if length > maxLength {
		ellipsisRunes := []rune(ellipsis)
		ellipsisLength := len(ellipsisRunes)
		if maxLength <= ellipsisLength {
			runes = ellipsisRunes[:maxLength]
		} else {
			runes = append(runes[:maxLength-ellipsisLength], ellipsisRunes...)
		}
		length = maxLength
	}
	if length < minLength {
		if padding == "" {
			padding = " "
		}
		paddingRunes := []rune(padding)
		paddingLength := len(paddingRunes)
		missingLength := minLength - length
		for i := range missingLength {
			runes = append(runes, paddingRunes[i%paddingLength])
		}
	}
	return string(runes)
}

var nonAsciiRegex = regexp.MustCompile(`[^\x00-\x7f]`)

func stripNonAscii(text string) string {
	return nonAsciiRegex.ReplaceAllString(text, "")
}

var yearSuffixRegex = regexp.MustCompile(`\s\(\d{4}\)$`)

func stripYearSuffix(text string) string {
	return yearSuffixRegex.ReplaceAllString(text, "")
}
