package jellyfin

import (
	"context"
	"crypto/rand"
	"drpp/server/api"
	"drpp/server/logger"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

const authRequestTimeout = 8 * time.Second

type Controller struct{}

func NewController() *Controller {
	return &Controller{}
}

func (c *Controller) RegisterRoutes(server *api.Server) {
	api.RegisterRoute(server, c.AuthenticateByPassword, http.MethodPost, "jellyfin/auth/password", http.StatusOK)
	api.RegisterRoute(server, c.InitiateQuickConnect, http.MethodPost, "jellyfin/auth/quick-connect", http.StatusOK)
	api.RegisterRoute(server, c.PollQuickConnect, http.MethodPost, "jellyfin/auth/quick-connect/poll", http.StatusOK)
}

type AuthResult struct {
	Authenticated bool   `json:"authenticated"`
	Url           string `json:"url,omitzero"`
	ServerName    string `json:"serverName,omitzero"`
	UserName      string `json:"userName,omitzero"`
	Token         string `json:"token,omitzero"`
	DeviceId      string `json:"deviceId,omitzero"`
}

type PasswordAuthInput struct {
	Url      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (c *Controller) AuthenticateByPassword(ctx context.Context, input PasswordAuthInput) (*AuthResult, error) {
	client, err := newAuthClient(input.Url, "")
	if err != nil {
		return nil, err
	}
	if input.Username == "" {
		return nil, api.ErrBadRequest("Username is required", nil)
	}
	result, err := client.AuthenticateByName(ctx, input.Username, input.Password)
	if err != nil {
		return nil, authError(err, "Sign-in failed")
	}
	return finishAuth(ctx, client, result)
}

type QuickConnectInitiateInput struct {
	Url string `json:"url"`
}

type QuickConnectInitiateResult struct {
	Url      string `json:"url"`
	Code     string `json:"code"`
	Secret   string `json:"secret"`
	DeviceId string `json:"deviceId"`
}

func (c *Controller) InitiateQuickConnect(ctx context.Context, input QuickConnectInitiateInput) (*QuickConnectInitiateResult, error) {
	client, err := newAuthClient(input.Url, "")
	if err != nil {
		return nil, err
	}
	result, err := client.InitiateQuickConnect(ctx)
	if err != nil {
		if statusErr, ok := errors.AsType[*HttpStatusError](err); ok && statusErr.StatusCode == http.StatusUnauthorized {
			return nil, api.ErrBadRequest("Quick Connect is disabled on this server", nil)
		}
		return nil, authError(err, "Failed to initiate Quick Connect")
	}
	return &QuickConnectInitiateResult{Url: client.BaseUrl, Code: result.Code, Secret: result.Secret, DeviceId: client.DeviceId}, nil
}

type QuickConnectPollInput struct {
	Url      string `json:"url"`
	Secret   string `json:"secret"`
	DeviceId string `json:"deviceId"`
}

func (c *Controller) PollQuickConnect(ctx context.Context, input QuickConnectPollInput) (*AuthResult, error) {
	client, err := newAuthClient(input.Url, input.DeviceId)
	if err != nil {
		return nil, err
	}
	state, err := client.GetQuickConnectState(ctx, input.Secret)
	if err != nil {
		return nil, authError(err, "Failed to check Quick Connect state")
	}
	if !state.Authenticated {
		return &AuthResult{Authenticated: false}, nil
	}
	result, err := client.AuthenticateWithQuickConnect(ctx, input.Secret)
	if err != nil {
		return nil, authError(err, "Quick Connect sign-in failed")
	}
	return finishAuth(ctx, client, result)
}

func newAuthClient(serverUrl string, deviceId string) (*Client, error) {
	parsed, err := url.Parse(serverUrl)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, api.ErrBadRequest("Invalid server URL, expected e.g. http://192.168.1.10:8096", nil)
	}
	if deviceId == "" {
		deviceId = "drpp-" + rand.Text()
	}
	return NewClient(serverUrl, "", deviceId, authRequestTimeout), nil
}

func finishAuth(ctx context.Context, client *Client, result *AuthenticationResult) (*AuthResult, error) {
	if result.AccessToken == "" {
		return nil, api.ErrBadRequest("Server did not return an access token", nil)
	}
	serverName := client.BaseUrl
	if info, err := client.GetPublicSystemInfo(ctx); err == nil && info.ServerName != "" {
		serverName = info.ServerName
	}
	return &AuthResult{
		Authenticated: true,
		Url:           client.BaseUrl,
		ServerName:    serverName,
		UserName:      result.User.Name,
		Token:         result.AccessToken,
		DeviceId:      client.DeviceId,
	}, nil
}

func authError(err error, message string) error {
	if statusErr, ok := errors.AsType[*HttpStatusError](err); ok {
		if statusErr.StatusCode == http.StatusUnauthorized {
			return api.ErrBadRequest(message+": invalid credentials", nil)
		}
		return api.ErrBadRequest(fmt.Sprintf("%s: %s", message, statusErr.Status), nil)
	}
	logger.Debug("Jellyfin auth error: %v", err)
	return api.ErrBadRequest(fmt.Sprintf("%s: could not reach server", message), []string{err.Error()})
}
