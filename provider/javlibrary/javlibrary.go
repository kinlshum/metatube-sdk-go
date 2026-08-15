package javlibrary

import (
	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/providerbridge"
)

const (
	Name     = "JavLibrary"
	Priority = 994
)

func New() *providerbridge.Provider {
	return providerbridge.NewGeneric(Name, "https://www.javlibrary.com/", Priority)
}

func init() { provider.Register(Name, New) }
