BINARY=marill
.DEFAULT_GOAL: $(BINARY)


GOPATH := $(shell go env | grep GOPATH | sed 's/GOPATH="\(.*\)"/\1/')
PATH := $(GOPATH)/bin:$(PATH)
export $(PATH)

$(BINARY):
	go get -u github.com/Masterminds/glide
	glide install
	# add tests to bindata.go for inclusion
	go-bindata tests/...
	go build -v -o ${BINARY}
