# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
BINARY_NAME=agent
BINARY_UNIX=$(BINARY_NAME)_unix

build:
	$(GOBUILD) -o $(BINARY_NAME) -v
