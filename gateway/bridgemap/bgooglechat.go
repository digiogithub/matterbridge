//go:build !nogooglechat
// +build !nogooglechat

package bridgemap

import (
	bgooglechat "github.com/42wim/matterbridge/bridge/googlechat"
)

func init() {
	FullMap["googlechat"] = bgooglechat.New
}
