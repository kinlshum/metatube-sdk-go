package engine

import (
	"context"
	"fmt"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm/clause"

	"github.com/metatube-community/metatube-sdk-go/engine/providerid"
	"github.com/metatube-community/metatube-sdk-go/internal/trace"
	"github.com/metatube-community/metatube-sdk-go/model"
	mt "github.com/metatube-community/metatube-sdk-go/provider"
)

func (e *Engine) getMovieReviewsFromDB(ctx context.Context, provider mt.MovieProvider, id string) (*model.MovieReviewInfo, error) {
	info := &model.MovieReviewInfo{}
	err := e.db. // Exact match here.
			Where("provider = ?", provider.Name()).
			Where("id = ? COLLATE NOCASE", id).
			First(info).Error
	traceCacheLookup(ctx, "reviews", provider.Name(), err == nil, boolToCount(err == nil), err)
	return info, err
}

func (e *Engine) getMovieReviewsWithCallback(ctx context.Context, provider mt.MovieProvider, id string, lazy bool,
	callback func() ([]*model.MovieReviewDetail, error),
) (info *model.MovieReviewInfo, err error) {
	defer func() {
		// metadata validation check.
		if err == nil && (info == nil || !info.IsValid()) {
			err = mt.ErrIncompleteMetadata
		}
	}()
	// Query DB first (by id).
	if lazy {
		if info, err = e.getMovieReviewsFromDB(ctx, provider, id); err == nil && info.IsValid() {
			return // ignore DB query error.
		}
	}
	// delayed info auto-save.
	defer func() {
		if err == nil && info.IsValid() {
			result := e.db.Clauses(clause.OnConflict{
				UpdateAll: true,
			}).Create(info) // ignore error
			trace.Emit(ctx, trace.Event{
				Component: trace.ComponentDatabase,
				Stage:     trace.StageCacheLookup,
				Provider:  provider.Name(),
				Details: trace.JSONMap{
					"source": "reviews",
					"action": "save",
					"rows":   result.RowsAffected,
				},
			})
		}
	}()

	var reviews []*model.MovieReviewDetail
	started := time.Now()
	reviews, err = callback()
	traceProviderResult(ctx, provider.Name(), started, err, trace.JSONMap{
		"operation":    "reviews",
		"review_count": len(reviews),
	})
	if err != nil {
		return
	}

	info = &model.MovieReviewInfo{
		ID:       id,
		Provider: provider.Name(),
		Reviews:  datatypes.NewJSONSlice(reviews),
	}
	return
}

func (e *Engine) getMovieReviewsByProviderID(ctx context.Context, provider mt.MovieProvider, id string, lazy bool) (*model.MovieReviewInfo, error) {
	if id = provider.NormalizeMovieID(id); id == "" {
		return nil, mt.ErrInvalidID
	}

	reviewer, ok := provider.(mt.MovieReviewer)
	if !ok {
		return nil, fmt.Errorf("reviews not supported by %s", provider.Name())
	}

	return e.getMovieReviewsWithCallback(ctx, provider, id, lazy, func() ([]*model.MovieReviewDetail, error) {
		release := e.providerThrottle.Begin(ctx, provider.Name())
		defer release()
		return reviewer.GetMovieReviewsByID(id)
	})
}

// GetMovieReviewsByProviderID returns movie reviews by provider ID.
func (e *Engine) GetMovieReviewsByProviderID(pid providerid.ProviderID, lazy bool) (*model.MovieReviewInfo, error) {
	return e.GetMovieReviewsByProviderIDContext(context.Background(), pid, lazy)
}

// GetMovieReviewsByProviderIDContext is the context-aware variant of
// GetMovieReviewsByProviderID.
func (e *Engine) GetMovieReviewsByProviderIDContext(ctx context.Context, pid providerid.ProviderID, lazy bool) (*model.MovieReviewInfo, error) {
	provider, err := e.GetMovieProviderByName(pid.Provider)
	if err != nil {
		return nil, err
	}
	return e.getMovieReviewsByProviderID(ctx, provider, pid.ID, lazy)
}

func (e *Engine) getMovieReviewsByProviderURL(ctx context.Context, provider mt.MovieProvider, rawURL string, lazy bool) (*model.MovieReviewInfo, error) {
	id, err := provider.ParseMovieIDFromURL(rawURL)
	switch {
	case err != nil:
		return nil, err
	case id == "":
		return nil, mt.ErrInvalidURL
	}

	reviewer, ok := provider.(mt.MovieReviewer)
	if !ok {
		return nil, fmt.Errorf("reviews not supported by %s", provider.Name())
	}

	return e.getMovieReviewsWithCallback(ctx, provider, id, lazy, func() ([]*model.MovieReviewDetail, error) {
		release := e.providerThrottle.Begin(ctx, provider.Name())
		defer release()
		return reviewer.GetMovieReviewsByURL(rawURL)
	})
}

// GetMovieReviewsByProviderURL returns movie reviews by provider URL.
func (e *Engine) GetMovieReviewsByProviderURL(rawURL string, lazy bool) (*model.MovieReviewInfo, error) {
	return e.GetMovieReviewsByProviderURLContext(context.Background(), rawURL, lazy)
}

// GetMovieReviewsByProviderURLContext is the context-aware variant of
// GetMovieReviewsByProviderURL.
func (e *Engine) GetMovieReviewsByProviderURLContext(ctx context.Context, rawURL string, lazy bool) (*model.MovieReviewInfo, error) {
	provider, err := e.GetMovieProviderByURL(rawURL)
	if err != nil {
		return nil, err
	}
	return e.getMovieReviewsByProviderURL(ctx, provider, rawURL, lazy)
}
