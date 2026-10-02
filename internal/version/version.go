/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package version holds the build-time version string, stamped into the
// operator and server binaries by GoReleaser (see .goreleaser.yaml's
// ldflags -X). Stays "dev" for any locally-built binary (make build,
// go build, go run), since those never pass the -X flag.
package version

var Version = "dev"
