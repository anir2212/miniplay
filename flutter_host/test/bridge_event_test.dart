import 'package:flutter_test/flutter_test.dart';
import 'package:miniplay_host/bridge_event.dart';

void main() {
  test('parses valid finish event', () {
    final event = BridgeEvent.parse('{"type":"miniplay","version":1,"event":"finish","game":"rhythm","score":40}');
    expect(event.kind, 'finish');
    expect(event.score, 40);
  });

  test('rejects malformed and forged bridge messages', () {
    expect(() => BridgeEvent.parse('{"type":"other","version":1,"event":"finish","game":"rhythm"}'), throwsFormatException);
    expect(() => BridgeEvent.parse('{"type":"miniplay","version":1,"event":"progress","game":"reflex","taps":-2}'), throwsFormatException);
  });
}
