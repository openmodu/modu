TAGS := sqlite_fts5

.PHONY: build run clean setup

setup:
	go run -tags "$(TAGS)" . setup

build:
	go build -tags "$(TAGS)" -o mmq .

run:
	go run -tags "$(TAGS)" .

clean:
	rm -f mmq
