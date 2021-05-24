module github.com/outblocks/cli-plugin-gcp

go 1.16

require (
	cloud.google.com/go/storage v1.10.0
	github.com/golang/protobuf v1.5.1 // indirect
	github.com/mitchellh/mapstructure v1.4.1
	github.com/outblocks/outblocks-plugin-go v0.0.0-00010101000000-000000000000
	golang.org/x/net v0.0.0-20210316092652-d523dce5a7f4 // indirect
	golang.org/x/oauth2 v0.0.0-20210313182246-cd4f82c27b84 // indirect
	google.golang.org/api v0.30.0
	google.golang.org/appengine v1.6.7 // indirect
)

replace github.com/outblocks/outblocks-plugin-go => ../outblocks-plugin-go
