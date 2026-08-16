package xslist

import (
	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/actorbridge"
)

const (
	Name     = "XsList"
	Priority = 1000
)

func New() *actorbridge.Provider {
	return actorbridge.New(Name, "https://xslist.org/", Priority)
}

func init() { provider.Register(Name, New) }
