package jellyfin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"drpp/server/logger"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	clientName       = "Discord Rich Presence"
	deviceName       = "DRPP"
	maxRespBodyBytes = 8 * 1024 * 1024 // 8 MB
)

// Set by main to the application version
var ClientVersion = "0.0.0"

type Client struct {
	BaseUrl    string
	Token      string
	DeviceId   string
	httpClient *http.Client
}

func NewClient(baseUrl string, token string, deviceId string, timeout time.Duration) *Client {
	if deviceId == "" {
		// Access tokens are bound to device IDs, so derive a stable one from the token if none was stored
		hash := sha256.Sum256([]byte(token))
		deviceId = "drpp-" + hex.EncodeToString(hash[:8])
	}
	return &Client{
		BaseUrl:    strings.TrimRight(baseUrl, "/"),
		Token:      token,
		DeviceId:   deviceId,
		httpClient: &http.Client{Timeout: timeout},
	}
}

func (c *Client) authorizationHeader() string {
	header := fmt.Sprintf(`MediaBrowser Client="%s", Device="%s", DeviceId="%s", Version="%s"`, clientName, deviceName, c.DeviceId, ClientVersion)
	if c.Token != "" {
		header += fmt.Sprintf(`, Token="%s"`, c.Token)
	}
	return header
}

type HttpStatusError struct {
	StatusCode int
	Status     string
}

func (e *HttpStatusError) Error() string {
	return "HTTP status " + e.Status
}

func (c *Client) request(ctx context.Context, method string, path string, body any, result any) error {
	var bodyReader io.Reader
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}
	reqUrl := c.BaseUrl + path
	req, err := http.NewRequestWithContext(ctx, method, reqUrl, bodyReader)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", c.authorizationHeader())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxRespBodyBytes))
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		logger.Debug("%s %s, HTTP %d, %s", method, reqUrl, resp.StatusCode, string(respBytes))
		return &HttpStatusError{StatusCode: resp.StatusCode, Status: resp.Status}
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(respBytes, result); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	return nil
}

type PublicSystemInfo struct {
	ServerName string `json:"ServerName"`
	Version    string `json:"Version"`
	Id         string `json:"Id"`
}

func (c *Client) GetPublicSystemInfo(ctx context.Context) (*PublicSystemInfo, error) {
	var info PublicSystemInfo
	if err := c.request(ctx, http.MethodGet, "/System/Info/Public", nil, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

type User struct {
	Name string `json:"Name"`
	Id   string `json:"Id"`
}

func (c *Client) GetCurrentUser(ctx context.Context) (*User, error) {
	var user User
	if err := c.request(ctx, http.MethodGet, "/Users/Me", nil, &user); err != nil {
		return nil, err
	}
	return &user, nil
}

type AuthenticationResult struct {
	User        User   `json:"User"`
	AccessToken string `json:"AccessToken"`
	ServerId    string `json:"ServerId"`
}

func (c *Client) AuthenticateByName(ctx context.Context, username string, password string) (*AuthenticationResult, error) {
	var result AuthenticationResult
	body := map[string]string{"Username": username, "Pw": password}
	if err := c.request(ctx, http.MethodPost, "/Users/AuthenticateByName", body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

type QuickConnectResult struct {
	Secret        string `json:"Secret"`
	Code          string `json:"Code"`
	Authenticated bool   `json:"Authenticated"`
}

func (c *Client) InitiateQuickConnect(ctx context.Context) (*QuickConnectResult, error) {
	var result QuickConnectResult
	err := c.request(ctx, http.MethodPost, "/QuickConnect/Initiate", nil, &result)
	if statusErr, ok := errors.AsType[*HttpStatusError](err); ok && (statusErr.StatusCode == http.StatusMethodNotAllowed || statusErr.StatusCode == http.StatusNotFound) {
		// Jellyfin versions before 10.9 use GET
		err = c.request(ctx, http.MethodGet, "/QuickConnect/Initiate", nil, &result)
	}
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) GetQuickConnectState(ctx context.Context, secret string) (*QuickConnectResult, error) {
	var result QuickConnectResult
	if err := c.request(ctx, http.MethodGet, "/QuickConnect/Connect?secret="+url.QueryEscape(secret), nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) AuthenticateWithQuickConnect(ctx context.Context, secret string) (*AuthenticationResult, error) {
	var result AuthenticationResult
	body := map[string]string{"Secret": secret}
	if err := c.request(ctx, http.MethodPost, "/Users/AuthenticateWithQuickConnect", body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

type Session struct {
	Id             string    `json:"Id"`
	UserId         string    `json:"UserId"`
	UserName       string    `json:"UserName"`
	Client         string    `json:"Client"`
	DeviceName     string    `json:"DeviceName"`
	NowPlayingItem *Item     `json:"NowPlayingItem"`
	PlayState      PlayState `json:"PlayState"`
}

type PlayState struct {
	PositionTicks int64 `json:"PositionTicks"`
	IsPaused      bool  `json:"IsPaused"`
}

func (c *Client) GetSessions(ctx context.Context) ([]Session, error) {
	var sessions []Session
	if err := c.request(ctx, http.MethodGet, "/Sessions?activeWithinSeconds=960", nil, &sessions); err != nil {
		return nil, err
	}
	return sessions, nil
}

type NameIdPair struct {
	Name string `json:"Name"`
	Id   string `json:"Id"`
}

type Item struct {
	Id                    string            `json:"Id"`
	Name                  string            `json:"Name"`
	Type                  string            `json:"Type"`
	ProductionYear        int64             `json:"ProductionYear"`
	IndexNumber           int64             `json:"IndexNumber"`
	ParentIndexNumber     int64             `json:"ParentIndexNumber"`
	RunTimeTicks          int64             `json:"RunTimeTicks"`
	Genres                []string          `json:"Genres"`
	ProviderIds           map[string]string `json:"ProviderIds"`
	ImageTags             map[string]string `json:"ImageTags"`
	SeriesId              string            `json:"SeriesId"`
	SeriesName            string            `json:"SeriesName"`
	SeriesPrimaryImageTag string            `json:"SeriesPrimaryImageTag"`
	SeasonId              string            `json:"SeasonId"`
	SeasonName            string            `json:"SeasonName"`
	AlbumId               string            `json:"AlbumId"`
	Album                 string            `json:"Album"`
	AlbumPrimaryImageTag  string            `json:"AlbumPrimaryImageTag"`
	AlbumArtist           string            `json:"AlbumArtist"`
	AlbumArtists          []NameIdPair      `json:"AlbumArtists"`
	Artists               []string          `json:"Artists"`
	EpisodeTitle          string            `json:"EpisodeTitle"`
	CurrentProgram        *Item             `json:"CurrentProgram"`
	// Full item object as returned by Jellyfin, exposed to templates
	Raw map[string]any `json:"-"`
}

func (c *Client) GetItem(ctx context.Context, itemId string, userId string) (*Item, error) {
	path := "/Items/" + url.PathEscape(itemId)
	if userId != "" {
		path += "?userId=" + url.QueryEscape(userId)
	}
	var raw jsontext.Value
	if err := c.request(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
	}
	var item Item
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, fmt.Errorf("unmarshal item: %w", err)
	}
	if err := json.Unmarshal(raw, &item.Raw); err != nil {
		return nil, fmt.Errorf("unmarshal raw item: %w", err)
	}
	return &item, nil
}

func (c *Client) GetAncestors(ctx context.Context, itemId string, userId string) ([]Item, error) {
	path := "/Items/" + url.PathEscape(itemId) + "/Ancestors"
	if userId != "" {
		path += "?userId=" + url.QueryEscape(userId)
	}
	var ancestors []Item
	if err := c.request(ctx, http.MethodGet, path, nil, &ancestors); err != nil {
		return nil, err
	}
	return ancestors, nil
}

func (c *Client) GetThumbUrl(thumb string) (string, map[string]string) {
	return c.BaseUrl + thumb, map[string]string{"Authorization": c.authorizationHeader()}
}

func thumbPath(itemId string, tag string) string {
	if itemId == "" || tag == "" {
		return ""
	}
	return "/Items/" + url.PathEscape(itemId) + "/Images/Primary?tag=" + url.QueryEscape(tag)
}
