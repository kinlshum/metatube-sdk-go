package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm/clause"

	"github.com/metatube-community/metatube-sdk-go/collection/sets"
	"github.com/metatube-community/metatube-sdk-go/collection/slices"
	"github.com/metatube-community/metatube-sdk-go/common/comparer"
	"github.com/metatube-community/metatube-sdk-go/common/number"
	"github.com/metatube-community/metatube-sdk-go/engine/providerid"
	"github.com/metatube-community/metatube-sdk-go/internal/trace"
	"github.com/metatube-community/metatube-sdk-go/model"
	mt "github.com/metatube-community/metatube-sdk-go/provider"
)

func (e *Engine) searchMovieFromDB(ctx context.Context, keyword string, provider mt.MovieProvider, all bool) (results []*model.MovieSearchResult, err error) {
	var infos []*model.MovieInfo
	tx := e.db.
		// Note: keyword might be an ID or just a regular number, so we should
		// query both of them for best match. Also, case should not matter.
		Where("number = ? COLLATE NOCASE", keyword).
		Or("id = ? COLLATE NOCASE", keyword)
	if all {
		err = tx.Find(&infos).Error
	} else {
		err = e.db.
			Where("provider = ?", provider.Name()).
			Where(tx).
			Find(&infos).Error
	}
	if err == nil {
		for _, info := range infos {
			if !info.IsValid() {
				// normally it is valid, but just in case.
				continue
			}
			results = append(results, info.ToSearchResult())
		}
	}
	traceCacheLookup(ctx, "search", providerName(provider), err == nil, len(results), err)
	return
}

func (e *Engine) searchMovie(ctx context.Context, keyword string, provider mt.MovieProvider, fallback bool) (results []*model.MovieSearchResult, err error) {
	// Regular keyword searching.
	if searcher, ok := provider.(mt.MovieSearcher); ok {
		normalized := searcher.NormalizeMovieKeyword(keyword)
		if normalized == "" {
			return nil, mt.ErrInvalidKeyword
		}
		if normalized != keyword {
			trace.Emit(ctx, trace.Event{
				Component: trace.ComponentMetaTube,
				Stage:     trace.StageNormalized,
				Provider:  provider.Name(),
				Details: trace.JSONMap{
					"query":            trace.SanitizeQuery(keyword),
					"normalized_query": trace.SanitizeQuery(normalized),
				},
			})
			keyword = normalized
		}
		if fallback {
			defer func() {
				innerResults, innerErr := e.searchMovieFromDB(ctx, keyword, provider, false)
				traceFallbackResult(ctx, provider.Name(), len(innerResults), innerErr)
				if innerErr == nil && len(innerResults) > 0 {
					// overwrite error.
					err = nil
					// update results.
					msr := sets.NewOrderedSetWithHash(func(v *model.MovieSearchResult) string { return v.Provider + v.ID })
					msr.Add(results...)
					msr.Add(innerResults...)
					results = msr.AsSlice()
				}
			}()
		}
		release := e.providerThrottle.Begin(ctx, provider.Name())
		defer release()
		started := time.Now()
		results, err = searcher.SearchMovie(keyword)
		traceProviderResult(ctx, provider.Name(), started, err, trace.JSONMap{
			"operation":    "search",
			"result_count": len(results),
		})
		return results, err
	}
	// Fallback to movie info querying.
	return e.searchMovieInfo(ctx, provider, keyword)
}

// searchMovieInfo queries a single movie by keyword when the provider does not
// support keyword searching. Provider events are recorded by the info path.
func (e *Engine) searchMovieInfo(ctx context.Context, provider mt.MovieProvider, keyword string) ([]*model.MovieSearchResult, error) {
	info, err := e.getMovieInfoByProviderID(ctx, provider, keyword, true)
	if err != nil {
		return nil, err
	}
	return []*model.MovieSearchResult{info.ToSearchResult()}, nil
}

// SearchMovie searches a single provider.
func (e *Engine) SearchMovie(keyword, name string, fallback bool) ([]*model.MovieSearchResult, error) {
	return e.SearchMovieContext(context.Background(), keyword, name, fallback)
}

// SearchMovieContext searches a single provider while recording the attempt
// against the trace carried by ctx.
func (e *Engine) SearchMovieContext(ctx context.Context, keyword, name string, fallback bool) ([]*model.MovieSearchResult, error) {
	if keyword = number.Trim(keyword); keyword == "" {
		return nil, mt.ErrInvalidKeyword
	}
	provider, err := e.GetMovieProviderByName(name)
	if err != nil {
		return nil, err
	}
	return e.searchMovie(ctx, keyword, provider, fallback)
}

func (e *Engine) searchMovieAll(ctx context.Context, keyword string) (results []*model.MovieSearchResult, err error) {
	type response struct {
		Results   []*model.MovieSearchResult
		Error     error
		Provider  mt.MovieProvider
		StartTime time.Time
		EndTime   time.Time
	}
	respCh := make(chan response)

	var wg sync.WaitGroup
	for _, provider := range e.movieProviders.Iterator() {
		wg.Add(1)
		// Goroutine started time.
		startTime := time.Now()
		// Async searching.
		go func(provider mt.MovieProvider) {
			defer wg.Done()
			innerResults, innerErr := e.searchMovie(ctx, keyword, provider, false)
			respCh <- response{
				Results:   innerResults,
				Error:     innerErr,
				Provider:  provider,
				StartTime: startTime,
				EndTime:   time.Now(),
			}
		}(provider)
	}
	go func() {
		wg.Wait()
		// notify when all searching tasks done.
		close(respCh)
	}()

	ds := make([]string, 0, e.movieProviders.Len())
	// response channel.
	for resp := range respCh {
		ds = append(ds, func(a, b, c any) string {
			if c == nil {
				c = "no error"
			}
			return fmt.Sprintf("%s(%s):<%v>", a, b, c)
		}(
			resp.Provider.Name(),
			resp.EndTime.Sub(resp.StartTime),
			resp.Error,
		))

		if resp.Error != nil {
			continue
		}
		results = append(results, resp.Results...)
	}

	e.logger.Printf("Search keyword %s: %s", keyword, strings.Join(ds, " | "))
	return
}

// SearchMovieAll searches the keyword from all providers.
func (e *Engine) SearchMovieAll(keyword string, fallback bool) ([]*model.MovieSearchResult, error) {
	return e.SearchMovieAllContext(context.Background(), keyword, fallback)
}

// SearchMovieAllContext searches all providers while recording each provider
// attempt, the database fallback and the selected result against ctx.
func (e *Engine) SearchMovieAllContext(ctx context.Context, keyword string, fallback bool) (results []*model.MovieSearchResult, err error) {
	if keyword = number.Trim(keyword); keyword == "" {
		return nil, mt.ErrInvalidKeyword
	}

	defer func() {
		if err != nil {
			return
		}
		if len(results) == 0 {
			err = mt.ErrInfoNotFound
			return
		}
		// remove duplicate results, if any.
		msr := sets.NewOrderedSetWithHash(func(v *model.MovieSearchResult) string { return v.Provider + v.ID })
		msr.Add(results...)
		results = msr.AsSlice()
		// post-processing
		ps := new(slices.WeightedSlice[*model.MovieSearchResult, float64])
		for _, result := range results {
			if !result.IsValid() /* validation check */ {
				continue
			}
			if _, err := e.GetMovieProviderByName(result.Provider); err != nil {
				e.logger.Printf("ignore provider %s as not found", result.Provider)
				continue
			}
			priority := comparer.Compare(keyword, result.Number) *
				e.MustGetMovieProviderByName(result.Provider).Priority()
			ps.Append(result, priority)
		}
		// sort by priority.
		results = ps.SortFunc(sort.Stable).Slice()
		if len(results) > 0 {
			traceSelection(ctx, results[0].Provider, results[0].ID, len(results))
		}
	}()

	if fallback /* query database for missing results  */ {
		defer func() {
			innerResults, innerErr := e.searchMovieFromDB(ctx, keyword, nil, true)
			traceFallbackResult(ctx, "", len(innerResults), innerErr)
			// ignore DB query error.
			if innerErr == nil && len(innerResults) > 0 {
				// overwrite error.
				err = nil
				// append results.
				results = append(results, innerResults...)
			}
		}()
	}

	results, err = e.searchMovieAll(ctx, keyword)
	return
}

func (e *Engine) getMovieInfoFromDB(ctx context.Context, provider mt.MovieProvider, id string) (*model.MovieInfo, error) {
	info := &model.MovieInfo{}
	err := e.db. // Exact match here.
			Where("provider = ?", provider.Name()).
			Where("id = ? COLLATE NOCASE", id).
			First(info).Error
	traceCacheLookup(ctx, "info", providerName(provider), err == nil, boolToCount(err == nil), err)
	return info, err
}

func (e *Engine) getMovieInfoWithCallback(ctx context.Context, provider mt.MovieProvider, id string, lazy bool, callback func() (*model.MovieInfo, error)) (info *model.MovieInfo, err error) {
	defer func() {
		// metadata validation check.
		if err == nil && (info == nil || !info.IsValid()) {
			err = mt.ErrIncompleteMetadata
		}
	}()
	// Query DB first (by id).
	if lazy {
		if info, err = e.getMovieInfoFromDB(ctx, provider, id); err == nil && info.IsValid() {
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
					"source": "info",
					"action": "save",
					"rows":   result.RowsAffected,
				},
			})
		}
	}()
	started := time.Now()
	info, err = callback()
	traceProviderResult(ctx, provider.Name(), started, err, trace.JSONMap{
		"operation": "info",
		"lazy":      lazy,
	})
	if err == nil && info != nil {
		traceSelection(ctx, provider.Name(), id, 1)
	}
	return info, err
}

func (e *Engine) getMovieInfoByProviderID(ctx context.Context, provider mt.MovieProvider, id string, lazy bool) (*model.MovieInfo, error) {
	if id = provider.NormalizeMovieID(id); id == "" {
		return nil, mt.ErrInvalidID
	}
	return e.getMovieInfoWithCallback(ctx, provider, id, lazy, func() (*model.MovieInfo, error) {
		release := e.providerThrottle.Begin(ctx, provider.Name())
		defer release()
		return provider.GetMovieInfoByID(id)
	})
}

// GetMovieInfoByProviderID returns movie metadata by provider ID.
func (e *Engine) GetMovieInfoByProviderID(pid providerid.ProviderID, lazy bool) (*model.MovieInfo, error) {
	return e.GetMovieInfoByProviderIDContext(context.Background(), pid, lazy)
}

// GetMovieInfoByProviderIDContext records cache lookups, the provider call and
// the selected result against the trace carried by ctx.
func (e *Engine) GetMovieInfoByProviderIDContext(ctx context.Context, pid providerid.ProviderID, lazy bool) (*model.MovieInfo, error) {
	provider, err := e.GetMovieProviderByName(pid.Provider)
	if err != nil {
		return nil, err
	}
	return e.getMovieInfoByProviderID(ctx, provider, pid.ID, lazy)
}

func (e *Engine) getMovieInfoByProviderURL(ctx context.Context, provider mt.MovieProvider, rawURL string, lazy bool) (*model.MovieInfo, error) {
	id, err := provider.ParseMovieIDFromURL(rawURL)
	switch {
	case err != nil:
		return nil, err
	case id == "":
		return nil, mt.ErrInvalidURL
	}
	return e.getMovieInfoWithCallback(ctx, provider, id, lazy, func() (*model.MovieInfo, error) {
		release := e.providerThrottle.Begin(ctx, provider.Name())
		defer release()
		return provider.GetMovieInfoByURL(rawURL)
	})
}

// GetMovieInfoByURL returns movie metadata by provider URL.
func (e *Engine) GetMovieInfoByURL(rawURL string, lazy bool) (*model.MovieInfo, error) {
	return e.GetMovieInfoByProviderURLContext(context.Background(), rawURL, lazy)
}

// GetMovieInfoByProviderURLContext is the context-aware variant of
// GetMovieInfoByURL.
func (e *Engine) GetMovieInfoByProviderURLContext(ctx context.Context, rawURL string, lazy bool) (*model.MovieInfo, error) {
	provider, err := e.GetMovieProviderByURL(rawURL)
	if err != nil {
		return nil, err
	}
	return e.getMovieInfoByProviderURL(ctx, provider, rawURL, lazy)
}

func boolToCount(value bool) int {
	if value {
		return 1
	}
	return 0
}
