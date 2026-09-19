package engine

import (
	"context"
	goerr "errors"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/language"

	"github.com/metatube-community/metatube-sdk-go/database"
	"github.com/metatube-community/metatube-sdk-go/engine/providerid"
	"github.com/metatube-community/metatube-sdk-go/internal/trace"
	"github.com/metatube-community/metatube-sdk-go/model"
	"github.com/metatube-community/metatube-sdk-go/provider/gfriends"
)

// fakeActorProvider stands in for a real provider so these tests never touch the
// network. It implements mt.ActorProvider and mt.ActorSearcher.
type fakeActorProvider struct {
	name      string
	tag       language.Tag
	info      *model.ActorInfo
	infoErr   error
	results   []*model.ActorSearchResult
	searchErr error
}

func (p *fakeActorProvider) Name() string                      { return p.name }
func (p *fakeActorProvider) Priority() float64                 { return 1 }
func (p *fakeActorProvider) SetPriority(float64)               {}
func (p *fakeActorProvider) Language() language.Tag            { return p.tag }
func (p *fakeActorProvider) NormalizeActorID(id string) string { return id }
func (p *fakeActorProvider) ParseActorIDFromURL(rawURL string) (string, error) {
	return rawURL, nil
}
func (p *fakeActorProvider) GetActorInfoByID(string) (*model.ActorInfo, error) {
	return p.info, p.infoErr
}
func (p *fakeActorProvider) GetActorInfoByURL(string) (*model.ActorInfo, error) {
	return p.info, p.infoErr
}
func (p *fakeActorProvider) SearchActor(string) ([]*model.ActorSearchResult, error) {
	return p.results, p.searchErr
}
func (p *fakeActorProvider) URL() *url.URL {
	parsed, _ := url.Parse("https://provider.test/" + p.name)
	return parsed
}

func testTraceService(t *testing.T) *trace.Service {
	t.Helper()
	service := trace.NewService(trace.Config{
		Enabled:         true,
		DSN:             filepath.Join(t.TempDir(), "traces.db"),
		RetentionDays:   trace.DefaultRetentionDays,
		MaxRuns:         trace.DefaultMaxRuns,
		MaxEventsPerRun: trace.DefaultMaxEventsPerRun,
		PruneInterval:   trace.MinPruneInterval,
	})
	require.True(t, service.Enabled())
	t.Cleanup(func() { _ = service.Close() })
	return service
}

func testEngine(t *testing.T, service *trace.Service) *Engine {
	t.Helper()
	db, err := database.Open(&database.Config{
		DSN:                  filepath.Join(t.TempDir(), "metatube.db"),
		DisableAutomaticPing: true,
	})
	require.NoError(t, err)
	app := New(db, WithTraceService(service))
	require.NoError(t, app.DBAutoMigrate(true))
	return app
}

// A provider failure may return (nil, error). The GFriends image-injection trace
// must not dereference that result, because tracing may never break a lookup.
func TestGFriendsImageInjectionWithNilResultDoesNotPanic(t *testing.T) {
	service := testTraceService(t)
	app := testEngine(t, service)

	japanese := &fakeActorProvider{
		name: "FakeJapanese",
		tag:  language.Japanese,
		info: &model.ActorInfo{
			ID:       "actor-1",
			Name:     "Saeki Yumika",
			Provider: "FakeJapanese",
			Homepage: "https://provider.test/FakeJapanese/actor-1",
		},
	}
	// Exactly the (nil, err) shape that used to panic the tracing code.
	broken := &fakeActorProvider{
		name:    gfriends.Name,
		tag:     language.English,
		info:    nil,
		infoErr: goerr.New("gfriends unavailable"),
	}
	app.actorProviders.Set(japanese.name, japanese)
	app.actorProviders.Set(gfriends.Name, broken)

	handle, ok := service.Start(trace.StartInput{
		Kind:       trace.KindActor,
		Operation:  trace.OperationIdentify,
		Query:      "actor-1",
		ClientName: "test",
	})
	require.True(t, ok)
	ctx := trace.WithContext(context.Background(), handle)

	require.NotPanics(t, func() {
		info, err := app.GetActorInfoByProviderIDContext(ctx,
			providerid.ProviderID{Provider: japanese.name, ID: "actor-1"}, true)
		require.NoError(t, err)
		require.NotNil(t, info)
		assert.Empty(t, info.Images, "no images can be injected from a failed lookup")
	})

	detail, err := service.Get(handle.TraceID())
	require.NoError(t, err)

	found := false
	for _, event := range detail.Events {
		// The throttle stage also carries the gfriends name; only the provider
		// stage reports the injection outcome.
		if event.Component != trace.ComponentProvider || event.Provider != gfriends.Name {
			continue
		}
		found = true
		assert.Equal(t, trace.LevelError, event.Level, "the failure must still be recorded")
		assert.EqualValues(t, 0, event.Details["result_count"], "a nil result counts as zero images")
		assert.EqualValues(t, "image_injection", event.Details["operation"])
	}
	assert.True(t, found, "the image-injection attempt must appear in the timeline")
}

// A successful actor search must persist the exact result count, and a search
// with no matches must keep a real zero.
func TestActorSearchPersistsResultCount(t *testing.T) {
	service := testTraceService(t)
	app := testEngine(t, service)

	matching := &fakeActorProvider{
		name: "FakeSearch",
		tag:  language.Japanese,
		results: []*model.ActorSearchResult{
			{ID: "a1", Name: "Saeki Yumika", Provider: "FakeSearch", Homepage: "https://provider.test/a1"},
			{ID: "a2", Name: "Saeki Yumika 2", Provider: "FakeSearch", Homepage: "https://provider.test/a2"},
		},
	}
	app.actorProviders.Set(matching.name, matching)

	handle, ok := service.Start(trace.StartInput{
		Kind:       trace.KindActor,
		Operation:  trace.OperationLookup,
		Query:      "Saeki Yumika",
		ClientName: "test",
	})
	require.True(t, ok)
	ctx := trace.WithContext(context.Background(), handle)

	results, err := app.SearchActorContext(ctx, "Saeki Yumika", matching.name, false)
	require.NoError(t, err)
	require.Len(t, results, 2)

	detail, err := service.Get(handle.TraceID())
	require.NoError(t, err)
	assert.Equal(t, 2, detail.Run.ResultCount)
	assert.Equal(t, "FakeSearch", detail.Run.SelectedProvider)
	assert.Equal(t, "a1", detail.Run.SelectedProviderID)

	// Empty search: nothing is selected, so the persisted count stays a real zero.
	empty := &fakeActorProvider{name: "FakeEmpty", tag: language.Japanese}
	app.actorProviders.Set(empty.name, empty)
	emptyHandle, ok := service.Start(trace.StartInput{
		Kind:       trace.KindActor,
		Operation:  trace.OperationLookup,
		Query:      "nobody",
		ClientName: "test",
	})
	require.True(t, ok)
	emptyCtx := trace.WithContext(context.Background(), emptyHandle)
	_, err = app.SearchActorContext(emptyCtx, "nobody", empty.name, false)
	require.Error(t, err)

	emptyDetail, err := service.Get(emptyHandle.TraceID())
	require.NoError(t, err)
	assert.Equal(t, 0, emptyDetail.Run.ResultCount)
	assert.Empty(t, emptyDetail.Run.SelectedProvider)
}
