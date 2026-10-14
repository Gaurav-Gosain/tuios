.DEFAULT_GOAL := test

.PHONY: fmt vet build test build-binary

#Format all Go source files in the project
fmt:
	@echo "Formatting...✨"
	@go fmt ./...
	@echo "Formatted successfully ✅"

#Check for suspicious constructs and common Go mistakes
vet: fmt #run fmt before vet
	@echo "Vetting...🔎"
	@go vet ./...
	@echo "Vetting passed ✅"

#Compile all packages to catch build errors without producing binaries
build: vet #run vet before build
	@echo "Building...🔨"
	@go build ./...
	@echo "Built successfully ✅"

#Run the full test suite without using cache test results
test: build #Run build before test
	@echo "Testing...🧪"
	go test ./... -count=1 -timeout 20m

#Build the tuios CLI into a local executable for manual testing
#The resulting ./tuios binary is written to the project root
build-binary:
	@echo "Building local binary... 🔨"
	@go build -o tuios ./cmd/tuios
	@echo "Built ./tuios. Run it with ./tuios in your terminal"
