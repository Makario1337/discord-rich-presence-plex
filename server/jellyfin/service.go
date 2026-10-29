package jellyfin

import (
	"context"
	"drpp/server/config"
	"drpp/server/logger"
	"drpp/server/media"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

const ticksPerMs = 10000

type Service struct {
	serverConfig  config.JellyfinServer
	fatalShutdown context.CancelFunc
	logger        *logger.Prefixed

	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewService(serverConfig config.JellyfinServer, fatalShutdown context.CancelFunc) *Service {
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

	client := NewClient(s.serverConfig.Url, s.serverConfig.Token.Value(), s.serverConfig.DeviceId, time.Duration(s.serverConfig.RequestTimeoutSeconds)*time.Second)

	info, err := client.GetPublicSystemInfo(ctx)
	if err != nil {
		return fmt.Errorf("fetch server info: %w", err)
	}

	listenForUser := s.serverConfig.ListenForUser
	user, err := client.GetCurrentUser(ctx)
	if err != nil {
		// API keys aren't associated with a user
		if listenForUser == "" {
			return fmt.Errorf("fetch current user (target username must be set when using an API key): %w", err)
		}
		s.logger.Info("Connected to server %q (v%s) without a user, listening for user %q", info.ServerName, info.Version, listenForUser)
	} else {
		if listenForUser == "" {
			listenForUser = user.Name
		}
		s.logger.Info("Connected to server %q (v%s) as user %q, listening for user %q", info.ServerName, info.Version, user.Name, listenForUser)
	}

	itemCache := make(map[string]*Item)
	getItem := func(itemId string, userId string, itemType string) *Item {
		if itemId == "" {
			return nil
		}
		if val, ok := itemCache[itemId]; ok {
			return val
		}
		item, err := client.GetItem(ctx, itemId, userId)
		if err != nil {
			s.logger.Error(err, "Failed to fetch metadata for %s item %q", itemType, itemId)
			return nil
		}
		itemCache[itemId] = item
		return item
	}
	libraryCache := make(map[string]string)
	getLibraryName := func(itemId string, userId string) string {
		if val, ok := libraryCache[itemId]; ok {
			return val
		}
		ancestors, err := client.GetAncestors(ctx, itemId, userId)
		if err != nil {
			s.logger.Error(err, "Failed to fetch ancestors for item %q", itemId)
			return ""
		}
		var libraryName string
		for _, ancestor := range ancestors {
			if ancestor.Type == "CollectionFolder" {
				libraryName = strings.TrimSpace(ancestor.Name)
				break
			}
		}
		libraryCache[itemId] = libraryName
		return libraryName
	}

	tracker := media.Tracker{SeekToleranceMs: int64(s.serverConfig.PollIntervalSeconds) * 1000}

	poll := func() error {
		sessions, err := client.GetSessions(ctx)
		if err != nil {
			return fmt.Errorf("fetch sessions: %w", err)
		}
		now := time.Now()

		var candidates []Session
		for _, session := range sessions {
			if session.NowPlayingItem != nil && strings.EqualFold(session.UserName, listenForUser) {
				candidates = append(candidates, session)
			}
		}
		sessionIndex := slices.IndexFunc(candidates, func(session Session) bool {
			return session.Id == tracker.SessionKey()
		})
		if sessionIndex == -1 {
			sessionIndex = slices.IndexFunc(candidates, func(session Session) bool {
				return !session.PlayState.IsPaused
			})
		}
		if sessionIndex == -1 && len(candidates) > 0 {
			sessionIndex = 0
		}

		if sessionIndex == -1 {
			if stopped := tracker.Stop(now); stopped != nil {
				callback(stopped)
			}
			clear(itemCache)
			clear(libraryCache)
			return nil
		}

		session := candidates[sessionIndex]
		state := "playing"
		if session.PlayState.IsPaused {
			state = "paused"
		}

		itemChanged, send := tracker.Observe(media.Observation{
			SessionKey: session.Id,
			ItemKey:    session.NowPlayingItem.Id,
			State:      state,
			PositionMs: session.PlayState.PositionTicks / ticksPerMs,
		}, now)
		if itemChanged {
			s.logger.Debug("Now playing item %q (%s) on %q (%s)", session.NowPlayingItem.Name, session.NowPlayingItem.Type, session.DeviceName, session.Client)
			clear(itemCache)
		}
		if !send {
			return nil
		}

		var activity *media.Activity
		if last := tracker.Last(); last != nil && !itemChanged {
			copied := *last
			activity = &copied
		} else {
			activity = s.buildActivity(session, getItem, getLibraryName)
			if activity == nil {
				tracker.Skipped(now)
				return nil
			}
			activity.GetThumbUrl = client.GetThumbUrl
		}
		activity.State = state
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

func (s *Service) buildActivity(session Session, getItem func(itemId string, userId string, itemType string) *Item, getLibraryName func(itemId string, userId string) string) *media.Activity {
	nowPlaying := session.NowPlayingItem
	var mediaType string
	switch nowPlaying.Type {
	case "Movie":
		mediaType = "movie"
	case "Episode":
		mediaType = "episode"
	case "Audio":
		mediaType = "track"
	case "MusicVideo", "Video", "Trailer":
		mediaType = "clip"
	case "TvChannel", "Program", "LiveTvProgram":
		mediaType = "liveEpisode"
	default:
		s.logger.Debug("Unrecognised item type %q, ignoring", nowPlaying.Type)
		return nil
	}

	libraryName := getLibraryName(nowPlaying.Id, session.UserId)
	if libraryName == "" {
		s.logger.Debug("Library name is empty, ignoring blacklist/whitelist")
	} else {
		if slices.ContainsFunc(s.serverConfig.BlacklistedLibraries, func(l string) bool {
			return strings.EqualFold(l, libraryName)
		}) {
			s.logger.Debug("Library %q is blacklisted, ignoring", libraryName)
			return nil
		}
		if len(s.serverConfig.WhitelistedLibraries) > 0 && !slices.ContainsFunc(s.serverConfig.WhitelistedLibraries, func(l string) bool {
			return strings.EqualFold(l, libraryName)
		}) {
			s.logger.Debug("Library %q is not whitelisted, ignoring", libraryName)
			return nil
		}
	}

	item := getItem(nowPlaying.Id, session.UserId, "main")
	if item == nil {
		// Fall back to the limited metadata included in the session
		item = nowPlaying
	}
	activity := &media.Activity{
		Source:      media.SourceJellyfin,
		MediaType:   mediaType,
		LibraryName: libraryName,
		Item:        item.toMedia(),
		RawItem:     item.rawOrSelf(),
	}

	switch mediaType {
	case "episode":
		activity.ParentItem = media.Metadata{Title: item.SeasonName, Index: item.ParentIndexNumber}
		if season := getItem(item.SeasonId, session.UserId, "parent"); season != nil {
			activity.ParentItem = season.toMedia()
			activity.RawParentItem = season.rawOrSelf()
		}
		activity.GrandparentItem = media.Metadata{Title: item.SeriesName, Thumb: thumbPath(item.SeriesId, item.SeriesPrimaryImageTag)}
		if series := getItem(item.SeriesId, session.UserId, "grandparent"); series != nil {
			activity.GrandparentItem = series.toMedia()
			activity.RawGrandparentItem = series.rawOrSelf()
		}
	case "track":
		activity.Item.Artist = strings.Join(item.Artists, ", ")
		activity.ParentItem = media.Metadata{Title: item.Album, Year: item.ProductionYear, Thumb: thumbPath(item.AlbumId, item.AlbumPrimaryImageTag)}
		if album := getItem(item.AlbumId, session.UserId, "parent"); album != nil {
			activity.ParentItem = album.toMedia()
			activity.RawParentItem = album.rawOrSelf()
		}
		activity.GrandparentItem = media.Metadata{Title: item.AlbumArtist}
		if len(item.AlbumArtists) > 0 {
			if artist := getItem(item.AlbumArtists[0].Id, session.UserId, "grandparent"); artist != nil {
				activity.GrandparentItem = artist.toMedia()
				activity.RawGrandparentItem = artist.rawOrSelf()
			}
		}
	case "liveEpisode":
		// Live TV sessions report the channel as the playing item
		show := media.Metadata{Title: item.Name, Thumb: activity.Item.Thumb}
		episodeTitle := item.EpisodeTitle
		if program := item.CurrentProgram; program != nil {
			show.Title = program.Name
			if thumb := thumbPath(program.Id, program.ImageTags["Primary"]); thumb != "" {
				show.Thumb = thumb
			}
			episodeTitle = program.EpisodeTitle
		}
		activity.GrandparentItem = show
		activity.Item.Title = episodeTitle
	}

	return activity
}

func (i *Item) toMedia() media.Metadata {
	result := media.Metadata{
		Title:      i.Name,
		Year:       i.ProductionYear,
		Index:      i.IndexNumber,
		Thumb:      thumbPath(i.Id, i.ImageTags["Primary"]),
		DurationMs: i.RunTimeTicks / ticksPerMs,
		Genres:     i.Genres,
	}
	// Tracks and albums also carry the MusicBrainz IDs of related items, so pick the one matching the item type
	var musicBrainzProvider string
	switch i.Type {
	case "Audio":
		musicBrainzProvider = "musicbrainztrack"
	case "MusicAlbum":
		musicBrainzProvider = "musicbrainzalbum"
	case "MusicArtist":
		musicBrainzProvider = "musicbrainzartist"
	}
	for provider, id := range i.ProviderIds {
		if id == "" {
			continue
		}
		var key string
		switch provider = strings.ToLower(provider); provider {
		case "imdb", "tmdb", "tvdb":
			key = provider
		case musicBrainzProvider:
			key = "mbid"
		default:
			continue
		}
		if result.ProviderIds == nil {
			result.ProviderIds = make(map[string]string)
		}
		result.ProviderIds[key] = id
	}
	return result
}

func (i *Item) rawOrSelf() any {
	if i.Raw != nil {
		return i.Raw
	}
	return *i
}
