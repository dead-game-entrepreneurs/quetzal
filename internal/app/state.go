package app

import (
	"github.com/princebyyourlogic/quetzal/internal/quazalcrypto"
	"github.com/princebyyourlogic/quetzal/internal/state"
	"os"
)

// OpenState retains the existing API; key persistence is shared by adapters.
func OpenState(directory string) (*os.File, quazalcrypto.Key, error) { return state.Open(directory) }
func principalKey(directory string) (quazalcrypto.Key, error)        { return state.PrincipalKey(directory) }
func lockState(directory string) (*os.File, error)                   { return state.Lock(directory) }
