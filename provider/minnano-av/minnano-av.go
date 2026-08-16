package minnanoav

import (
	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/actorbridge"
)

const (
	Name     = "Minnano-AV"
	Priority = 998
)

func New() *actorbridge.Provider {
	return actorbridge.New(Name, "https://www.minnano-av.com/", Priority)
}

func init() { provider.Register(Name, New) }
