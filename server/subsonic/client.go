package subsonic

import (
	"context"
	"crypto/md5" //nolint:gosec // Required by the Subsonic token authentication scheme
	"crypto/rand"
	"drpp/server/logger"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	clientName       = "drpp"
	apiVersion       = "1.16.1"
	maxRespBodyBytes = 8 * 1024 * 1024 // 8 MB
)

type Client struct {
	BaseUrl    string
	Username   string
	Password   string
	ApiKey     string
	httpClient *http.Client
}

func NewClient(baseUrl string, username string, password string, apiKey string, timeout time.Duration) *Client {
	return &Client{
		BaseUrl:    strings.TrimRight(baseUrl, "/"),
		Username:   username,
		Password:   password,
		ApiKey:     apiKey,
		httpClient: &http.Client{Timeout: timeout},
	}
}

func (c *Client) authParams() url.Values {
	params := url.Values{}
	params.Set("v", apiVersion)
	params.Set("c", clientName)
	params.Set("f", "json")
	if c.ApiKey != "" {
		params.Set("apiKey", c.ApiKey)
		return params
	}
	salt := rand.Text()
	hash := md5.Sum([]byte(c.Password + salt)) //nolint:gosec
	params.Set("u", c.Username)
	params.Set("t", hex.EncodeToString(hash[:]))
	params.Set("s", salt)
	return params
}

func (c *Client) endpointUrl(endpoint string, params url.Values) string {
	query := c.authParams()
	for key, values := range params {
		query[key] = values
	}
	return c.BaseUrl + "/rest/" + endpoint + "?" + query.Encode()
}

type ApiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *ApiError) Error() string {
	return fmt.Sprintf("Subsonic error %d: %s", e.Code, e.Message)
}

// call fetches the given endpoint and unmarshals the given key of the response (or the whole response if key is empty) into result (and rawResult, if non-nil)
func (c *Client) call(ctx context.Context, endpoint string, params url.Values, key string, result any, rawResult *map[string]any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpointUrl(endpoint, params), nil)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			// Keep credentials out of logs
			urlErr.URL, _, _ = strings.Cut(urlErr.URL, "?")
		}
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxRespBodyBytes))
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		logger.Debug("GET %s/rest/%s, HTTP %d, %s", c.BaseUrl, endpoint, resp.StatusCode, string(respBytes))
		return fmt.Errorf("HTTP status %s", resp.Status)
	}
	var envelope struct {
		Response jsontext.Value `json:"subsonic-response"`
	}
	if err := json.Unmarshal(respBytes, &envelope); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	var fields struct {
		Status string    `json:"status"`
		Error  *ApiError `json:"error"`
	}
	if err := json.Unmarshal(envelope.Response, &fields); err != nil {
		return fmt.Errorf("unmarshal status: %w", err)
	}
	if fields.Status != "ok" {
		if fields.Error == nil {
			return fmt.Errorf("unexpected response status %q", fields.Status)
		}
		return fields.Error
	}
	value := envelope.Response
	if key != "" {
		var members map[string]jsontext.Value
		if err := json.Unmarshal(envelope.Response, &members); err != nil {
			return fmt.Errorf("unmarshal members: %w", err)
		}
		var ok bool
		if value, ok = members[key]; !ok {
			// Some servers omit empty objects
			value = jsontext.Value("{}")
		}
	}
	if err := json.Unmarshal(value, result); err != nil {
		return fmt.Errorf("unmarshal %s: %w", key, err)
	}
	if rawResult != nil {
		if err := json.Unmarshal(value, rawResult); err != nil {
			return fmt.Errorf("unmarshal raw %s: %w", key, err)
		}
	}
	return nil
}

type ServerInfo struct {
	Type          string `json:"type"`
	ServerVersion string `json:"serverVersion"`
	OpenSubsonic  bool   `json:"openSubsonic"`
}

func (c *Client) Ping(ctx context.Context) (*ServerInfo, error) {
	var info ServerInfo
	if err := c.call(ctx, "ping", nil, "", &info, nil); err != nil {
		return nil, err
	}
	return &info, nil
}

func (c *Client) GetTokenUsername(ctx context.Context) (string, error) {
	var tokenInfo struct {
		Username string `json:"username"`
	}
	if err := c.call(ctx, "tokenInfo", nil, "tokenInfo", &tokenInfo, nil); err != nil {
		return "", err
	}
	return tokenInfo.Username, nil
}

// flexString accepts JSON strings and numbers, since some Subsonic servers return IDs as numbers
type flexString string

func (s *flexString) UnmarshalJSON(data []byte) error {
	switch jsontext.Value(data).Kind() {
	case '"':
		var str string
		if err := json.Unmarshal(data, &str); err != nil {
			return err
		}
		*s = flexString(str)
	case '0':
		*s = flexString(data)
	case 'n':
		*s = ""
	default:
		return fmt.Errorf("unexpected JSON value %s for ID", data)
	}
	return nil
}

type ItemGenre struct {
	Name string `json:"name"`
}

type ArtistRef struct {
	Id   flexString `json:"id"`
	Name string     `json:"name"`
}

type NowPlayingEntry struct {
	Id            flexString  `json:"id"`
	Title         string      `json:"title"`
	Album         string      `json:"album"`
	AlbumId       flexString  `json:"albumId"`
	Artist        string      `json:"artist"`
	ArtistId      flexString  `json:"artistId"`
	Artists       []ArtistRef `json:"artists"`
	AlbumArtists  []ArtistRef `json:"albumArtists"`
	DisplayArtist string      `json:"displayArtist"`
	CoverArt      flexString  `json:"coverArt"`
	Duration      int64       `json:"duration"` // Seconds
	Year          int64       `json:"year"`
	Track         int64       `json:"track"`
	Genre         string      `json:"genre"`
	Genres        []ItemGenre `json:"genres"`
	MusicBrainzId string      `json:"musicBrainzId"`
	Type          string      `json:"type"`
	IsVideo       bool        `json:"isVideo"`
	Username      string      `json:"username"`
	MinutesAgo    int64       `json:"minutesAgo"`
	PlayerId      flexString  `json:"playerId"`
	PlayerName    string      `json:"playerName"`
	// playbackReport extension
	State        string         `json:"state"`
	PositionMs   *int64         `json:"positionMs"`
	PlaybackRate float64        `json:"playbackRate"`
	Raw          map[string]any `json:"-"`
}

func (c *Client) GetNowPlaying(ctx context.Context) ([]NowPlayingEntry, error) {
	var nowPlaying struct {
		Entry []jsontext.Value `json:"entry"`
	}
	if err := c.call(ctx, "getNowPlaying", nil, "nowPlaying", &nowPlaying, nil); err != nil {
		return nil, err
	}
	entries := make([]NowPlayingEntry, 0, len(nowPlaying.Entry))
	for _, raw := range nowPlaying.Entry {
		var entry NowPlayingEntry
		if err := json.Unmarshal(raw, &entry); err != nil {
			return nil, fmt.Errorf("unmarshal entry: %w", err)
		}
		if err := json.Unmarshal(raw, &entry.Raw); err != nil {
			return nil, fmt.Errorf("unmarshal raw entry: %w", err)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

type Album struct {
	Id            flexString     `json:"id"`
	Name          string         `json:"name"`
	Artist        string         `json:"artist"`
	ArtistId      flexString     `json:"artistId"`
	CoverArt      flexString     `json:"coverArt"`
	Year          int64          `json:"year"`
	Genre         string         `json:"genre"`
	Genres        []ItemGenre    `json:"genres"`
	MusicBrainzId string         `json:"musicBrainzId"`
	Raw           map[string]any `json:"-"`
}

func (c *Client) GetAlbum(ctx context.Context, id string) (*Album, error) {
	var album Album
	if err := c.call(ctx, "getAlbum", url.Values{"id": {id}}, "album", &album, &album.Raw); err != nil {
		return nil, err
	}
	delete(album.Raw, "song") // Not useful in templates, and can be large
	return &album, nil
}

type Artist struct {
	Id            flexString     `json:"id"`
	Name          string         `json:"name"`
	CoverArt      flexString     `json:"coverArt"`
	MusicBrainzId string         `json:"musicBrainzId"`
	Raw           map[string]any `json:"-"`
}

func (c *Client) GetArtist(ctx context.Context, id string) (*Artist, error) {
	var artist Artist
	if err := c.call(ctx, "getArtist", url.Values{"id": {id}}, "artist", &artist, &artist.Raw); err != nil {
		return nil, err
	}
	delete(artist.Raw, "album") // Not useful in templates, and can be large
	return &artist, nil
}

func (c *Client) GetThumbUrl(thumb string) (string, map[string]string) {
	// Thumbs are stored as "/rest/getCoverArt?id=<id>" so that they don't contain credentials (they're used as cache keys)
	_, id, _ := strings.Cut(thumb, "?id=")
	id, _ = url.QueryUnescape(id)
	return c.endpointUrl("getCoverArt", url.Values{"id": {id}}), nil
}

func thumbPath(coverArt flexString) string {
	if coverArt == "" {
		return ""
	}
	return "/rest/getCoverArt?id=" + url.QueryEscape(string(coverArt))
}

func genreNames(genres []ItemGenre, genre string) []string {
	if len(genres) == 0 {
		if genre == "" {
			return nil
		}
		return []string{genre}
	}
	names := make([]string, 0, len(genres))
	for _, g := range genres {
		names = append(names, g.Name)
	}
	return names
}

func (e *NowPlayingEntry) sessionKey() string {
	return e.Username + "/" + string(e.PlayerId) + "/" + strconv.Quote(e.PlayerName)
}
