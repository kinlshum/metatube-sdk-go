package javdb

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeMovieID(t *testing.T) {
	p := New()
	assert.Equal(t, "JUKD-074", p.NormalizeMovieID("JUKD-074_1080p.mp4"))
	assert.Equal(t, "T28-509", p.NormalizeMovieID("T28-509-RM"))
	assert.Equal(t, "200GANA-3414", p.NormalizeMovieID("200GANA-3414"))
}
