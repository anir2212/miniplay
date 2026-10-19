# MiniPlay Flutter host

This Flutter app opens the MiniPlay web client in a WebView. It is a host for the game, not a second scoring system. The page sends versioned `start`, `progress` and `finish` messages; `lib/bridge_event.dart` validates their shape before the host displays them. `lib/main.dart` restricts navigation to the configured game origin and supplies a **demo** user ID. The Go score service decides whether a run counts.

From this directory, run `flutter pub get`, `flutter analyze` and `flutter test`. For a local emulator, start the Go gateway and built web client first, then run `flutter run --dart-define=MINIPLAY_USER=alice`. Android emulator networking points to `http://10.0.2.2:8080`; an iOS simulator uses `http://127.0.0.1:8080`. Override with `--dart-define=MINIPLAY_URL=http://HOST:8080` for a different development host.

The bridge parser tests passed and Flutter analysis reported zero issues in the local evidence in [RESULTS.md](../RESULTS.md). A physical device or Android emulator game session is still unverified. Local cleartext HTTP and the demo identity are development settings; a shared release needs HTTPS and authenticated user identity.
