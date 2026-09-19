package engine

import (
	"context"
	goerr "errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"golang.org/x/text/language"
	"gorm.io/gorm/clause"

	"github.com/metatube-community/metatube-sdk-go/collection/sets"
	"github.com/metatube-community/metatube-sdk-go/collection/slices"
	"github.com/metatube-community/metatube-sdk-go/common/comparer"
	"github.com/metatube-community/metatube-sdk-go/common/parser"
	"github.com/metatube-community/metatube-sdk-go/engine/providerid"
	"github.com/metatube-community/metatube-sdk-go/internal/trace"
	"github.com/metatube-community/metatube-sdk-go/model"
	mt "github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/gfriends"
)

func (e *Engine) searchActorFromDB(ctx context.Context, keyword string, provider mt.Provider) (results []*model.ActorSearchResult, err error) {
	var infos []*model.ActorInfo
	if err = e.db.
		Where("provider = ? AND name = ? COLLATE NOCASE",
			provider.Name(), keyword).
		Find(&infos).Error; err == nil {
		for _, info := range infos {
			if !info.IsValid() {
				continue
			}
			results = append(results, info.ToSearchResult())
		}
	}
	traceCacheLookup(ctx, "search", provider.Name(), err == nil, len(results), err)
	return
}

func (e *Engine) searchActor(ctx context.Context, keyword string, provider mt.Provider, fallback bool) ([]*model.ActorSearchResult, error) {
	innerSearch := func(keyword string) (results []*model.ActorSearchResult, err error) {
		if provider.Name() == gfriends.Name {
			release := e.providerThrottle.Begin(ctx, provider.Name())
			defer release()
			started := time.Now()
			results, err = provider.(mt.ActorSearcher).SearchActor(keyword)
			traceProviderResult(ctx, provider.Name(), started, err, trace.JSONMap{"operation": "search", "result_count": len(results)})
			return results, err
		}
		if searcher, ok := provider.(mt.ActorSearcher); ok {
			defer func() {
				if err != nil || len(results) == 0 {
					return // ignore error or empty.
				}
				const minSimilarity = 0.3
				ps := new(slices.WeightedSlice[*model.ActorSearchResult, float64])
				for _, result := range results {
					if similarity := comparer.Compare(result.Name, keyword); similarity >= minSimilarity {
						ps.Append(result, similarity)
					}
				}
				results = ps.SortFunc(sort.Stable).Slice() // replace results.
			}()
			if fallback {
				defer func() {
					innerResults, innerErr := e.searchActorFromDB(ctx, keyword, provider)
					traceFallbackResult(ctx, provider.Name(), len(innerResults), innerErr)
					if innerErr == nil && len(innerResults) > 0 {
						// overwrite error.
						err = nil
						// update results.
						asr := sets.NewOrderedSetWithHash(func(v *model.ActorSearchResult) string { return v.Provider + v.ID })
						// unlike movie searching, we want search results go first
						// than DB data here, so we add results later than DB results.
						asr.Add(innerResults...)
						asr.Add(results...)
						results = asr.AsSlice()
					}
				}()
			}
			release := e.providerThrottle.Begin(ctx, provider.Name())
			defer release()
			started := time.Now()
			results, err = searcher.SearchActor(keyword)
			traceProviderResult(ctx, provider.Name(), started, err, trace.JSONMap{"operation": "search", "result_count": len(results)})
			return results, err
		}
		// All providers should implement the ActorSearcher interface.
		return nil, mt.ErrInfoNotFound
	}
	names := parser.ParseActorNames(keyword)
	if len(names) == 0 {
		return nil, mt.ErrInvalidKeyword
	}
	var (
		results []*model.ActorSearchResult
		errors  []error
	)
	for _, name := range names {
		innerResults, innerErr := innerSearch(name)
		if innerErr != nil &&
			// ignore InfoNotFound error.
			!goerr.Is(innerErr, mt.ErrInfoNotFound) {
			// add error to chain and handle it later.
			errors = append(errors, innerErr)
			continue
		}
		results = append(results, innerResults...)
	}
	if len(results) == 0 {
		if len(errors) > 0 {
			return nil, fmt.Errorf("search errors: %v", errors)
		}
		return nil, mt.ErrInfoNotFound
	}
	return results, nil
}

// SearchActor searches a single actor provider.
func (e *Engine) SearchActor(keyword, name string, fallback bool) ([]*model.ActorSearchResult, error) {
	return e.SearchActorContext(context.Background(), keyword, name, fallback)
}

// SearchActorContext searches a single actor provider while recording the
// attempt against the trace carried by ctx.
func (e *Engine) SearchActorContext(ctx context.Context, keyword, name string, fallback bool) ([]*model.ActorSearchResult, error) {
	provider, err := e.GetActorProviderByName(name)
	if err != nil {
		return nil, err
	}
	results, err := e.searchActor(ctx, keyword, provider, fallback)
	if err == nil && len(results) > 0 {
		traceSelection(ctx, results[0].Provider, results[0].ID, len(results))
	}
	return results, err
}

// SearchActorAll searches every actor provider.
func (e *Engine) SearchActorAll(keyword string, fallback bool) ([]*model.ActorSearchResult, error) {
	return e.SearchActorAllContext(context.Background(), keyword, fallback)
}

// SearchActorAllContext searches every actor provider concurrently while
// recording each provider attempt against the trace carried by ctx.
func (e *Engine) SearchActorAllContext(ctx context.Context, keyword string, fallback bool) (results []*model.ActorSearchResult, err error) {
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for _, provider := range e.actorProviders.Iterator() {
		wg.Add(1)
		go func(provider mt.ActorProvider) {
			defer wg.Done()
			if innerResults, innerErr := e.searchActor(ctx, keyword, provider, fallback); innerErr == nil {
				for _, result := range innerResults {
					if result.IsValid() /* validation check */ {
						mu.Lock()
						results = append(results, result)
						mu.Unlock()
					}
				}
			} // ignore error
		}(provider)
	}
	wg.Wait()

	sort.SliceStable(results, func(i, j int) bool {
		return e.MustGetActorProviderByName(results[i].Provider).Priority() >
			e.MustGetActorProviderByName(results[j].Provider).Priority()
	})
	if len(results) > 0 {
		traceSelection(ctx, results[0].Provider, results[0].ID, len(results))
	}
	return
}

func (e *Engine) getActorInfoFromDB(ctx context.Context, provider mt.ActorProvider, id string) (*model.ActorInfo, error) {
	info := &model.ActorInfo{}
	err := e.db. // Exact match here.
			Where("provider = ?", provider.Name()).
			Where("id = ? COLLATE NOCASE", id).
			First(info).Error
	traceCacheLookup(ctx, "info", provider.Name(), err == nil, boolToCount(err == nil), err)
	return info, err
}

func (e *Engine) getActorInfoWithCallback(ctx context.Context, provider mt.ActorProvider, id string, lazy bool, callback func() (*model.ActorInfo, error)) (info *model.ActorInfo, err error) {
	defer func() {
		// metadata validation check.
		if err == nil && (info == nil || !info.IsValid()) {
			err = mt.ErrIncompleteMetadata
		}
	}()
	if provider.Name() == gfriends.Name {
		release := e.providerThrottle.Begin(ctx, provider.Name())
		defer release()
		started := time.Now()
		info, err = provider.GetActorInfoByID(id)
		traceProviderResult(ctx, provider.Name(), started, err, trace.JSONMap{"operation": "info"})
		if err == nil && info != nil {
			traceSelection(ctx, provider.Name(), id, 1)
		}
		return info, err
	}
	defer func() {
		// gfriends actor image injection for JAV actor providers.
		if err == nil && info != nil && provider.Language() == language.Japanese {
			release := e.providerThrottle.Begin(ctx, gfriends.Name)
			started := time.Now()
			gInfo, gErr := e.MustGetActorProviderByName(gfriends.Name).GetActorInfoByID(info.Name)
			release()
			// A failed provider call may return (nil, err): never dereference it,
			// and report zero images instead of panicking a metadata request.
			imageCount := 0
			if gInfo != nil {
				imageCount = len(gInfo.Images)
			}
			traceProviderResult(ctx, gfriends.Name, started, gErr, trace.JSONMap{
				"operation":    "image_injection",
				"result_count": imageCount,
			})
			if gErr == nil && imageCount > 0 {
				info.Images = append(gInfo.Images, info.Images...)
			}
		}
	}()
	// Query DB first (by id).
	if lazy {
		if info, err = e.getActorInfoFromDB(ctx, provider, id); err == nil && info.IsValid() {
			return
		}
	}
	// Delayed info auto-save.
	defer func() {
		if err == nil && info.IsValid() {
			// Make sure we save the original info here.
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

func (e *Engine) getActorInfoByProviderID(ctx context.Context, provider mt.ActorProvider, id string, lazy bool) (*model.ActorInfo, error) {
	if id = provider.NormalizeActorID(id); id == "" {
		return nil, mt.ErrInvalidID
	}
	return e.getActorInfoWithCallback(ctx, provider, id, lazy, func() (*model.ActorInfo, error) {
		release := e.providerThrottle.Begin(ctx, provider.Name())
		defer release()
		return provider.GetActorInfoByID(id)
	})
}

// GetActorInfoByProviderID returns actor metadata by provider ID.
func (e *Engine) GetActorInfoByProviderID(pid providerid.ProviderID, lazy bool) (*model.ActorInfo, error) {
	return e.GetActorInfoByProviderIDContext(context.Background(), pid, lazy)
}

// GetActorInfoByProviderIDContext records cache lookups, the provider call and
// the selected result against the trace carried by ctx.
func (e *Engine) GetActorInfoByProviderIDContext(ctx context.Context, pid providerid.ProviderID, lazy bool) (*model.ActorInfo, error) {
	provider, err := e.GetActorProviderByName(pid.Provider)
	if err != nil {
		return nil, err
	}
	return e.getActorInfoByProviderID(ctx, provider, pid.ID, lazy)
}

func (e *Engine) getActorInfoByProviderURL(ctx context.Context, provider mt.ActorProvider, rawURL string, lazy bool) (*model.ActorInfo, error) {
	id, err := provider.ParseActorIDFromURL(rawURL)
	switch {
	case err != nil:
		return nil, err
	case id == "":
		return nil, mt.ErrInvalidURL
	}
	return e.getActorInfoWithCallback(ctx, provider, id, lazy, func() (*model.ActorInfo, error) {
		release := e.providerThrottle.Begin(ctx, provider.Name())
		defer release()
		return provider.GetActorInfoByURL(rawURL)
	})
}

// GetActorInfoByURL returns actor metadata by provider URL.
func (e *Engine) GetActorInfoByURL(rawURL string, lazy bool) (*model.ActorInfo, error) {
	return e.GetActorInfoByProviderURLContext(context.Background(), rawURL, lazy)
}

// GetActorInfoByProviderURLContext is the context-aware variant of
// GetActorInfoByURL.
func (e *Engine) GetActorInfoByProviderURLContext(ctx context.Context, rawURL string, lazy bool) (*model.ActorInfo, error) {
	provider, err := e.GetActorProviderByURL(rawURL)
	if err != nil {
		return nil, err
	}
	return e.getActorInfoByProviderURL(ctx, provider, rawURL, lazy)
}
