package subsonic

import (
	"context"
	"drpp/server/config"
	"drpp/server/logger"
	"drpp/server/media"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// Without the playbackReport extension, entries can linger after playback ends, so they're considered stale this long after their estimated end
const staleEntryGracePeriod = 30 * time.Second

// Album/artist caches are cleared when they grow beyond this size, so that long sessions don't accumulate memory and failed fetches are eventually retried
const maxCacheSize = 100

type Service struct {
	serverConfig  config.SubsonicServer
	fatalShutdown context.CancelFunc
	logger        *logger.Prefixed

	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewService(serverConfig config.SubsonicServer, fatalShutdown context.CancelFunc) *Service {
	prefix := serverConfig.Name
	if prefix == "" {
		prefix = serverConfig.Url
	}
	return &Service{
		serverConfig:  serverConfig,
		fatalShutdown: fatalShutdown,
		logger:        logger.NewPrefixed(prefix),
	}
}

func (s *Service) Start(callback func(activity *media.Activity)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}
	var ctx context.Context
	ctx, s.cancel = context.WithCancel(context.Background())
	s.wg.Go(func() {
		var retries int
		for {
			err := s.run(ctx, callback)
			if ctx.Err() != nil {
				return
			}
			if err == nil {
				s.logger.Info("Reconnecting in %d seconds", s.serverConfig.RetryIntervalSeconds)
				retries = 0
			} else {
				s.logger.Error(err, "Failed to initialise")
				if s.serverConfig.MaxRetriesBeforeExit > -1 && retries >= s.serverConfig.MaxRetriesBeforeExit {
					s.logger.Error(nil, "Max retries exceeded, shutting down")
					s.fatalShutdown()
					return
				}
				s.logger.Info("Retrying in %d seconds", s.serverConfig.RetryIntervalSeconds)
				retries++
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(s.serverConfig.RetryIntervalSeconds) * time.Second):
			}
		}
	})
}

func (s *Service) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel == nil {
		return
	}
	s.cancel()
	s.cancel = nil
	s.wg.Wait()
	s.logger.Info("Disconnected")
}

func (s *Service) run(ctx context.Context, callback func(activity *media.Activity)) error {

	apiKey := s.serverConfig.ApiKey.Value()
	if apiKey == "" && s.serverConfig.Username == "" {
		return errors.New("either a username and password or an API key must be set")
	}
	client := NewClient(s.serverConfig.Url, s.serverConfig.Username, s.serverConfig.Password.Value(), apiKey, time.Duration(s.serverConfig.RequestTimeoutSeconds)*time.Second)

	info, err := client.Ping(ctx)
	if err != nil {
		return fmt.Errorf("ping server: %w", err)
	}

	listenForUser := s.serverConfig.ListenForUser
	if listenForUser == "" {
		if apiKey == "" {
			listenForUser = s.serverConfig.Username
		} else {
			listenForUser, err = client.GetTokenUsername(ctx)
			if err != nil {
				return fmt.Errorf("fetch API key username (or set the target username): %w", err)
			}
		}
	}
	serverType := info.Type
	if serverType == "" {
		serverType = "Subsonic"
	}
	s.logger.Info("Connected to %s server (v%s, OpenSubsonic: %t), listening for user %q", serverType, info.ServerVersion, info.OpenSubsonic, listenForUser)

	albumCache := make(map[string]*Album)
	getAlbum := func(id string) *Album {
		if id == "" {
			return nil
		}
		if val, ok := albumCache[id]; ok {
			return val
		}
		if len(albumCache) >= maxCacheSize {
			clear(albumCache)
		}
		album, err := client.GetAlbum(ctx, id)
		if err != nil {
			s.logger.Error(err, "Failed to fetch album %q", id)
		}
		albumCache[id] = album // Cache failures too, to avoid retrying on every poll
		return album
	}
	artistCache := make(map[string]*Artist)
	getArtist := func(id string) *Artist {
		if id == "" {
			return nil
		}
		if val, ok := artistCache[id]; ok {
			return val
		}
		if len(artistCache) >= maxCacheSize {
			clear(artistCache)
		}
		artist, err := client.GetArtist(ctx, id)
		if err != nil {
			s.logger.Error(err, "Failed to fetch artist %q", id)
		}
		artistCache[id] = artist
		return artist
	}

	tracker := media.Tracker{SeekToleranceMs: int64(s.serverConfig.PollIntervalSeconds) * 1000}
	// Local start time estimates for entries without a reported position
	startTimes := make(map[string]time.Time)

	poll := func() error {
		entries, err := client.GetNowPlaying(ctx)
		if err != nil {
			return fmt.Errorf("fetch now playing: %w", err)
		}
		now := time.Now()

		type candidate struct {
			entry      NowPlayingEntry
			state      string
			positionMs int64
		}
		var candidates []candidate
		seenKeys := make(map[string]bool)
		for _, entry := range entries {
			if !strings.EqualFold(entry.Username, listenForUser) || entry.IsVideo || (entry.Type != "" && entry.Type != "music") {
				continue
			}
			c := candidate{entry: entry, state: "playing"}
			switch entry.State {
			case "stopped":
				continue
			case "paused":
				c.state = "paused"
			}
			if entry.PositionMs != nil {
				c.positionMs = *entry.PositionMs
			} else {
				// No playbackReport support, so estimate the position from when the entry was first seen
				key := entry.sessionKey() + "/" + string(entry.Id)
				seenKeys[key] = true
				startTime, ok := startTimes[key]
				if !ok {
					startTime = now.Add(-time.Duration(entry.MinutesAgo) * time.Minute)
					startTimes[key] = startTime
				}
				c.positionMs = now.Sub(startTime).Milliseconds()
				if entry.Duration > 0 && now.Sub(startTime) > time.Duration(entry.Duration)*time.Second+staleEntryGracePeriod {
					continue
				}
				c.positionMs = min(c.positionMs, entry.Duration*1000)
			}
			candidates = append(candidates, c)
		}
		for key := range startTimes {
			if !seenKeys[key] {
				delete(startTimes, key)
			}
		}

		index := slices.IndexFunc(candidates, func(c candidate) bool {
			return c.entry.sessionKey() == tracker.SessionKey()
		})
		if index == -1 {
			index = slices.IndexFunc(candidates, func(c candidate) bool {
				return c.state == "playing"
			})
		}
		if index == -1 && len(candidates) > 0 {
			index = 0
		}

		if index == -1 {
			if stopped := tracker.Stop(now); stopped != nil {
				callback(stopped)
			}
			return nil
		}

		c := candidates[index]
		itemChanged, send := tracker.Observe(media.Observation{
			SessionKey: c.entry.sessionKey(),
			ItemKey:    string(c.entry.Id),
			State:      c.state,
			PositionMs: c.positionMs,
			Rate:       c.entry.PlaybackRate,
		}, now)
		if itemChanged {
			s.logger.Debug("Now playing track %q on player %q", c.entry.Title, c.entry.PlayerName)
			// Retry failed fetches for the new track
			for id, album := range albumCache {
				if album == nil {
					delete(albumCache, id)
				}
			}
			for id, artist := range artistCache {
				if artist == nil {
					delete(artistCache, id)
				}
			}
		}
		if !send {
			return nil
		}

		var activity *media.Activity
		if last := tracker.Last(); last != nil && !itemChanged {
			copied := *last
			activity = &copied
		} else {
			activity = buildActivity(&c.entry, getAlbum, getArtist)
			activity.GetThumbUrl = client.GetThumbUrl
		}
		activity.State = c.state
		activity.ElapsedDurationMs = tracker.EstimatedPositionMs(now)
		callback(tracker.Sent(activity, now))
		return nil
	}

	pollInterval := time.Duration(s.serverConfig.PollIntervalSeconds) * time.Second
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		if err := poll(); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			s.logger.Error(err, "Restarting due to polling error")
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}

}

func buildActivity(entry *NowPlayingEntry, getAlbum func(id string) *Album, getArtist func(id string) *Artist) *media.Activity {
	artist := entry.DisplayArtist
	if len(entry.Artists) > 0 {
		names := make([]string, 0, len(entry.Artists))
		for _, a := range entry.Artists {
			names = append(names, a.Name)
		}
		artist = strings.Join(names, ", ")
	}
	if artist == "" {
		artist = entry.Artist
	}
	activity := &media.Activity{
		Source:    media.SourceSubsonic,
		MediaType: "track",
		Item: media.Metadata{
			Title:       entry.Title,
			Artist:      artist,
			Year:        entry.Year,
			Index:       entry.Track,
			Thumb:       thumbPath(entry.CoverArt),
			DurationMs:  entry.Duration * 1000,
			Genres:      genreNames(entry.Genres, entry.Genre),
			ProviderIds: musicBrainzIds(entry.MusicBrainzId),
		},
		RawItem: entry.Raw,
		// Fallbacks in case the album/artist can't be fetched
		ParentItem:      media.Metadata{Title: entry.Album, Year: entry.Year, Thumb: thumbPath(entry.CoverArt)},
		GrandparentItem: media.Metadata{Title: entry.Artist},
	}

	artistId := string(entry.ArtistId)
	if len(entry.AlbumArtists) > 0 {
		artistId = string(entry.AlbumArtists[0].Id)
		activity.GrandparentItem.Title = entry.AlbumArtists[0].Name
	}
	if album := getAlbum(string(entry.AlbumId)); album != nil {
		activity.ParentItem = media.Metadata{
			Title:       album.Name,
			Year:        album.Year,
			Thumb:       thumbPath(album.CoverArt),
			Genres:      genreNames(album.Genres, album.Genre),
			ProviderIds: musicBrainzIds(album.MusicBrainzId),
		}
		activity.RawParentItem = album.Raw
		if album.ArtistId != "" {
			artistId = string(album.ArtistId)
		}
		if album.Artist != "" {
			activity.GrandparentItem.Title = album.Artist
		}
	}
	if artist := getArtist(artistId); artist != nil {
		activity.GrandparentItem = media.Metadata{
			Title:       artist.Name,
			Thumb:       thumbPath(artist.CoverArt),
			ProviderIds: musicBrainzIds(artist.MusicBrainzId),
		}
		activity.RawGrandparentItem = artist.Raw
	}
	return activity
}

func musicBrainzIds(id string) map[string]string {
	if id == "" {
		return nil
	}
	return map[string]string{"mbid": id}
}
