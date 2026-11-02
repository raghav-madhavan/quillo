#!/usr/bin/env sh
# Generates the test clips the local test addon serves. Run from the
# repository root: scripts/quillo-test-addon/make-media.sh
# Then: go run -tags quillotestaddon ./scripts/quillo-test-addon scripts/quillo-test-addon/media
# (the build tag keeps this dev tool out of normal builds and the route inventory)
# and install http://localhost:9100/manifest.json on Admin > Addons.
set -eu
dir=scripts/quillo-test-addon/media
mkdir -p "$dir"
ffmpeg -loglevel error -y -f lavfi -i testsrc2=size=1920x1080:rate=24 -f lavfi -i sine=frequency=330:sample_rate=48000 \
  -t 60 -c:v libx265 -preset ultrafast -tag:v hvc1 -pix_fmt yuv420p -c:a aac -b:a 128k "$dir/test-1080p-hevc.mkv"
ffmpeg -loglevel error -y -f lavfi -i testsrc2=size=1280x720:rate=24 -f lavfi -i sine=frequency=440:sample_rate=48000 \
  -t 90 -c:v libx264 -preset veryfast -pix_fmt yuv420p -c:a aac -b:a 128k -movflags +faststart "$dir/test-720p-h264.mp4"
echo "Test clips written to $dir"
