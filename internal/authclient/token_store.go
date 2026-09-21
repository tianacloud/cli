package authclient

import "github.com/tianacloud/sdk-go/auth"

type InstanceTokenCredential = auth.InstanceTokenCredential
type InstanceTokenStore = auth.InstanceTokenStore

var ErrInstanceTokenNotFound = auth.ErrInstanceTokenNotFound
var NewFileInstanceTokenStore = auth.NewFileInstanceTokenStore
var NewInstanceTokenStore = auth.NewInstanceTokenStore
var DefaultInstanceTokenPath = auth.DefaultInstanceTokenPath
