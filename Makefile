.PHONY: test vet build all release

TAG := go/v$(VERSION)
REPO := tptodorov/symphony

test vet build all:
	$(MAKE) -C go $@

release:
	@test -n "$(VERSION)" || { echo "usage: make release VERSION=0.1.0"; exit 2; }
	@case "$(VERSION)" in v*) echo "VERSION should omit the leading v, for example VERSION=0.1.0"; exit 2;; esac
	git town sync
	$(MAKE) -C go all
	gh release create $(TAG) --repo $(REPO) --target main --generate-notes --fail-on-no-commits
