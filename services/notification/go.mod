module github.com/jp-cloud/notification

go 1.22

require github.com/jp-cloud/go-common v0.0.0

require (
	github.com/golang-jwt/jwt/v5 v5.2.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
)

replace github.com/jp-cloud/go-common => ../../packages/go-common
