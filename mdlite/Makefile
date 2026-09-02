# mdlite is copied into its consumers rather than referenced: Go's embed
# refuses `..` and will not follow a symlink, and nginx only serves diagme/app.
# `install` pushes the copies out, `check` fails if one has drifted.
COPIES := ../diagme/app/md.js ../taskhound/md.js

.DEFAULT_GOAL := help

help: ## show this help
	@grep -hE '^[a-z-]+:.*##' $(MAKEFILE_LIST) | sed 's/:.*##/\t/' | expand -t12

test: ## run the renderer tests
	node test.js

install: ## copy md.js into every consumer
	@for dst in $(COPIES); do cp md.js $$dst && echo "updated $$dst"; done

check: test ## test, then fail if any copy has drifted from the source
	@status=0; for dst in $(COPIES); do \
	  if cmp -s md.js $$dst; then echo "ok      $$dst"; \
	  else echo "DRIFTED $$dst -- run: make -C mdlite install"; status=1; fi; \
	done; exit $$status

.PHONY: help test install check
