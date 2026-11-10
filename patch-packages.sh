#!/bin/sh
# Applies the patches in patches/ to the Go modules they name, as npm's patch-package does for
# packages: a fix a dependency's release does not have yet, kept as a diff rather than a copy.
#
# patches/<module path>+<version>.patch is a git-style diff against that module as released, so
# patches/github.com/emersion/go-smtp+v0.25.1.patch is go-smtp v0.25.1. Each is applied to a fresh
# copy of the release in .patched/<module path>, which git ignores, and go.mod is pointed at that
# copy with a replace line. go.mod has to require the version the patch is for.
#
# Run it after cloning and whenever a patch changes; the Dockerfile and CI run it before building.
# Offer each patch upstream, and delete it, and its replace line, once a release has it.
set -eu
root=$(cd "$(dirname "$0")" && pwd)
cd "$root"

for patch in $(find patches -name '*.patch' | sort); do
	name=${patch#patches/}
	name=${name%.patch}
	module=${name%+*}
	version=${name##*+}

	# From outside the module, so go.mod's replace does not answer for the release.
	src=$(cd / && go mod download -json "$module@$version" |
		sed -n 's/^[[:space:]]*"Dir": "\(.*\)",$/\1/p')
	if [ -z "$src" ]; then
		echo "$module@$version did not download" >&2
		exit 1
	fi

	dest=.patched/$module
	rm -rf "$dest"
	mkdir -p "$(dirname "$dest")"
	cp -R "$src" "$dest"
	# The module cache is read-only.
	chmod -R u+w "$dest"
	(cd "$dest" && patch -p1 -s <"$root/$patch")

	go mod edit -replace "$module=./$dest"
	required=$(go list -m -f '{{.Version}}' "$module")
	if [ "$required" != "$version" ]; then
		echo "go.mod requires $module $required, and $patch is for $version" >&2
		exit 1
	fi
	echo "$module $version: patched"
done
