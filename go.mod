module github.com/outblocks/cli-plugin-gcp

go 1.16

require (
	cloud.google.com/go/storage v1.15.0
	github.com/Microsoft/go-winio v0.5.0 // indirect
	github.com/containerd/containerd v1.5.2 // indirect
	github.com/docker/cli v20.10.6+incompatible // indirect
	github.com/docker/docker v20.10.6+incompatible
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/google/go-containerregistry v0.5.1
	github.com/mitchellh/mapstructure v1.4.1
	github.com/outblocks/outblocks-plugin-go v0.0.0-20210708161832-56a8cbbf879e
	github.com/sirupsen/logrus v1.8.1 // indirect
	golang.org/x/net v0.0.0-20210525063256-abc453219eb5 // indirect
	golang.org/x/oauth2 v0.0.0-20210514164344-f6687ab2804c
	golang.org/x/sync v0.0.0-20210220032951-036812b2e83c
	google.golang.org/api v0.48.0
)

// replace github.com/outblocks/outblocks-plugin-go => ../outblocks-plugin-go
