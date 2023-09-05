module github.com/roadrunner-server/sqs/v4

go 1.21

toolchain go1.21.0

require (
	github.com/aws/aws-sdk-go-v2 v1.21.0
	github.com/aws/aws-sdk-go-v2/config v1.18.39
	github.com/aws/aws-sdk-go-v2/credentials v1.13.37
	github.com/aws/aws-sdk-go-v2/service/sqs v1.24.5
	github.com/aws/smithy-go v1.14.2
	github.com/goccy/go-json v0.10.2
	github.com/google/uuid v1.3.1
	github.com/roadrunner-server/api/v4 v4.7.1
	github.com/roadrunner-server/endure/v2 v2.4.2
	github.com/roadrunner-server/errors v1.3.0
	github.com/stretchr/testify v1.8.4
	go.opentelemetry.io/contrib/propagators/jaeger v1.18.0
	go.opentelemetry.io/otel v1.17.0
	go.opentelemetry.io/otel/sdk v1.17.0
	go.opentelemetry.io/otel/trace v1.17.0
	go.uber.org/zap v1.25.0
)

require (
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.13.11 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.1.41 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.4.35 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.3.42 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.9.35 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.13.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.15.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.21.5 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/go-logr/logr v1.2.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	go.opentelemetry.io/otel/metric v1.17.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/sys v0.12.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
