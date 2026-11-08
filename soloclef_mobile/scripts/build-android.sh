#!/usr/bin/env bash
# Build a signed release APK locally, with the Android SDK inside Docker (the
# host needs only Docker and Node). The keystore and its passwords stay
# outside the repository.
#
#   SECRETS_DIR=/path/to/secrets OUT_DIR=/path/to/builds scripts/build-android.sh
#
# SECRETS_DIR must hold surfsense-release.jks and a gradle.properties with
# SOLOCLEF_RELEASE_STORE_FILE=/secrets/surfsense-release.jks and the
# SOLOCLEF_RELEASE_* passwords (see plugins/with-release-signing.js).
set -euo pipefail

APP_DIR="$(cd "$(dirname "$0")/.." && pwd)"
SECRETS_DIR="${SECRETS_DIR:-/home/danvics/docker/surfsense-mobile-secrets}"
OUT_DIR="${OUT_DIR:-/home/danvics/docker/surfsense-mobile-builds}"
IMAGE="${ANDROID_IMAGE:-reactnativecommunity/react-native-android:v21.1}"
GRADLE_CACHE_VOLUME="${GRADLE_CACHE_VOLUME:-soloclef-mobile-gradle}"

cd "$APP_DIR"
[ -d node_modules ] || npm ci
npx expo prebuild -p android --clean --no-install

docker run --rm \
	-v "$APP_DIR":/app \
	-v "$SECRETS_DIR":/secrets:ro \
	-v "$GRADLE_CACHE_VOLUME":/root/.gradle \
	-w /app/android \
	-e NODE_ENV=production \
	-e GRADLE_USER_HOME=/root/.gradle \
	"$IMAGE" \
	bash -c 'cp /secrets/gradle.properties /root/.gradle/gradle.properties; ./gradlew assembleRelease --no-daemon -PreactNativeArchitectures=arm64-v8a,armeabi-v7a,x86_64; rc=$?; rm -f /root/.gradle/gradle.properties; exit $rc'

# Refuse to publish an APK signed with the debug key.
CERTS="$(docker run --rm -v "$APP_DIR":/app "$IMAGE" bash -c \
	'BT=$ANDROID_HOME/build-tools/$(ls $ANDROID_HOME/build-tools | tail -1); $BT/apksigner verify --print-certs /app/android/app/build/outputs/apk/release/app-release.apk')"
echo "$CERTS" | grep "certificate DN"
if ! grep -q "CN=Soloclef Notes" <<<"$CERTS"; then
	echo "release APK is not signed with the Soloclef key" >&2
	exit 1
fi

VERSION="$(node -p "require('./app.json').expo.version")"
mkdir -p "$OUT_DIR"
TARGET="$OUT_DIR/soloclef-notes-${VERSION}-$(date +%Y%m%d-%H%M).apk"
cp android/app/build/outputs/apk/release/app-release.apk "$TARGET"
ln -sf "$(basename "$TARGET")" "$OUT_DIR/soloclef-notes-latest.apk"
echo "APK: $TARGET ($(du -h "$TARGET" | cut -f1))"
