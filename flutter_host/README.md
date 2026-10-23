# MiniPlay Flutter host

This small Flutter app opens the MiniPlay web client in a WebView. It is a host for the game, not a second scoring system. The page sends versioned `start`, `progress` and `finish` messages; `lib/bridge_event.dart` validates their shape before the host displays them. `lib/main.dart` restricts navigation to the configured game origin, refuses any certificate the platform does not trust, and supplies a **demo** user ID. The Go score service decides whether a run counts.

From this directory, run `flutter pub get`, `flutter analyze` and `flutter test`. For a local emulator, start the Go gateway and built web client first, then run `flutter run --dart-define=MINIPLAY_USER=alice`. Android emulator networking points to `http://10.0.2.2:8080`; an iOS simulator uses `http://127.0.0.1:8080`. Override with `--dart-define=MINIPLAY_URL=http://HOST:8080` for a different development host.

## Network policy on Android

Each Android build type gets its own network security config (`android/app/src/<build type>/res/xml/network_security_config.xml`):

| Build | Cleartext HTTP | Certificate authorities trusted |
|---|---|---|
| `debug` (`flutter run`) | Allowed, so a local `http://` gateway works | System |
| `release` and `profile` (production) | Refused | System only; user-installed CAs are not trusted |
| `localRelease` (non-production) | Refused | System, plus one local development CA for `10.0.2.2`, `127.0.0.1` and `localhost` only |

Until 23 October 2026 the main manifest set `android:usesCleartextTraffic="true"`, so every build, release included, allowed plain HTTP. Earlier notes said Flutter's Gradle plugin added that setting to debug builds only; that was wrong.

## Release builds

Release builds are signed with an upload key that is never committed. `android/app/build.gradle.kts` reads it from the `MINIPLAY_UPLOAD_STORE_FILE`, `MINIPLAY_UPLOAD_STORE_PASSWORD`, `MINIPLAY_UPLOAD_KEY_ALIAS` and `MINIPLAY_UPLOAD_KEY_PASSWORD` environment variables, or from an untracked properties file with the keys `storeFile`, `storePassword`, `keyAlias` and `keyPassword`. That file is `MINIPLAY_KEY_PROPERTIES` if set, otherwise `android/key.properties`, which is git-ignored, as are `*.jks` and `*.keystore`. Use absolute paths. Without a key, any release task stops with an error instead of producing a debug-signed APK.

```sh
keytool -genkeypair -keystore /somewhere/private/upload.p12 -storetype PKCS12 \
  -alias miniplay-upload -keyalg RSA -keysize 3072 -validity 10000
export MINIPLAY_KEY_PROPERTIES=/somewhere/private/key.properties   # storeFile=..., storePassword=..., keyAlias=..., keyPassword=...
flutter build apk --release --target-platform android-arm64 \
  --dart-define=MINIPLAY_URL=https://game.example --dart-define=MINIPLAY_USER=alice
```

The production build loads only an HTTPS gateway whose certificate chains to a CA in the system store.

### localRelease: release build against a local HTTPS gateway

`localRelease` exists to test a release build against a gateway on your own machine. It is the release build type with these differences:

- application ID `com.example.miniplay_host.local` and version suffix `-local`, so it can sit beside the production app;
- it trusts one extra CA, only for `10.0.2.2`, `127.0.0.1` and `localhost`;
- R8 shrinking is off. Flutter's Gradle plugin copies a custom build type's minify setting to plugin libraries, and minifying the WebView plugin library on its own strips a class the app needs.

It is still release-signed, non-debuggable and AOT-compiled. Do not distribute it. The CA certificate is copied in at build time from `MINIPLAY_LOCAL_CA_CERT` or `-Pminiplay.localCaCert`, so it is never stored in the repository. The build refuses a file that contains a private key or a certificate that is not a CA. Flutter's `build apk` has no option for a custom build type, so run Gradle the way `flutter build apk -v` does and change only the task:

```sh
cd android
./gradlew -Ptarget-platform=android-arm64 -Ptarget=lib/main.dart \
  -Pdart-defines=<the base64 list that flutter build apk -v prints> \
  -Pminiplay.localCaCert=/somewhere/private/ca.pem assembleLocalRelease
```

The gateway serves HTTPS when `MINIPLAY_TLS_CERT` and `MINIPLAY_TLS_KEY` are set (see the main README). Its certificate needs `10.0.2.2` as an IP subject alternative name for the emulator. Keep the CA's private key outside the repository.

## Device evidence

The bridge parser tests passed and Flutter analysis reported zero issues in the local evidence in [RESULTS.md](../RESULTS.md). On 22 October a debug build played one tapped game on an Android 14 emulator: the score reached the database and live board, and the app's board updated live when other players scored. Later that day a person played both games on a Samsung Galaxy S23 (Android 16) over USB, using `adb reverse tcp:18080` and `--dart-define=MINIPLAY_URL=http://127.0.0.1:18080`. Both scores reached the database, and the phone's board updated live. Those were debug builds over plain HTTP. On 24 October the `localRelease` build ran on the emulator against the gateway serving HTTPS with a local CA. It loaded the game over https, a tapped game (90 points) reached the database and the live board, and the app's board updated when two other players scored. The production release build, pointed at the same gateway, refused the certificate and showed "certificate rejected". Both APKs were release-signed and not debuggable. The production build has not yet loaded a page from a gateway with a publicly trusted certificate.

The demo identity is a development setting; a shared release needs authenticated user identity.
