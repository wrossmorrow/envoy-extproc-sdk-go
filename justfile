
default:
    just --list

# update dependendencies
update:
    go get -u && just tidy

# cleanup modules
tidy:
    go mod tidy

# format code
format:
    go fmt ./...

# run linter
lint:
    golangci-lint run ./...

# run vetter
vet:
    go vet -vettool=$(go env GOPATH)/bin/shadow ./...

# run unit tests
unit-test: 
    go test -v ./...

# run unit tests
test:
    go test ./...

# run integration tests
integration-test: 
    cd test && just up -d --wait --wait-timeout 90 && just test ; STATUS=$? ; just down --volumes ; exit ${STATUS}

# run tests with coverage (TBD)
coverage: 
    echo "TBD" && exit 1

# Run a k6 load test script from loadtest/ against the containerized stack
# (envoy -> extproc -> echo upstream). Usage: just loadtest <script> [k6 args...]
loadtest *args="smoke.js":
    docker pull grafana/k6
    docker run --rm --network host -v "$(pwd)/loadtest:/scripts" grafana/k6 run /scripts/{{args}}

# Run the k6 smoke load test against the full containerized envoy chain.
loadtest-smoke:
    just loadtest smoke.js

# Run the k6 steady-state load test (RPS via $RPS, duration via $DURATION).
loadtest-steady:
    just loadtest steady.js

# Run the k6 burst load test (VUs via $VUS).
loadtest-burst:
    just loadtest burst.js

# Run the k6 delayed/upstream-latency load test (ECHO_DELAY/ECHO_STATUS).
loadtest-delayed:
    just loadtest delayed.js

# Run all k6 load tests in loadtest/ (smoke, steady, burst, delayed).
loadtest-all:
    just loadtest smoke.js
    just loadtest steady.js
    just loadtest burst.js
    just loadtest delayed.js

# Run the plain gRPC load test against a running extproc server (no envoy in
# the path, no upstream). Drives full ext_proc exchanges over the raw gRPC
# server. Usage: just loadtest-grpc [iterations] [concurrency]
loadtest-grpc iterations concurrency:
    cd test && go run ./loadgen --iterations {{iterations}} --concurrency {{concurrency}}

# Run a single-exchange plain gRPC load test (smoke) against a running extproc
# server. Usage: just loadtest-grpc-smoke
loadtest-grpc-smoke:
    just loadtest-grpc 1 1

# Run a plain gRPC burst load test: N concurrent exchanges against a running
# extproc server. Usage: just loadtest-grpc-burst [concurrency]
loadtest-grpc-burst concurrency:
    just loadtest-grpc {{concurrency}} {{concurrency}}

# run a specific example
run example="noop":
    cd examples && just run {{example}}

# build binary (variadic flags supported)
build *flags="":
    go build {{flags}} ./...

# tag for a release
tag version="":
    git tag v{{version}} && git push origin --tags

# release a new version, via a specific commit (deprecate)
release version="":
    git commit -m "release v{{version}}" \
        && git push && just tag {{version}}
