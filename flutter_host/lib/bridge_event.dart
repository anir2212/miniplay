import 'dart:convert';

class BridgeEvent {
  const BridgeEvent({required this.kind, required this.game, this.taps, this.score, this.error});

  final String kind;
  final String game;
  final int? taps;
  final int? score;
  final String? error;

  static BridgeEvent parse(String encoded) {
    final value = jsonDecode(encoded);
    if (value is! Map<String, dynamic> ||
        value['type'] != 'miniplay' ||
        value['version'] != 1 ||
        !['start', 'progress', 'finish'].contains(value['event']) ||
        !['rhythm', 'reflex'].contains(value['game'])) {
      throw const FormatException('Unknown MiniPlay bridge event');
    }
    final kind = value['event'] as String;
    final taps = value['taps'];
    final score = value['score'];
    final error = value['error'];
    if ((kind == 'progress' && (taps is! int || taps < 0)) ||
        (kind == 'finish' && score != null && (score is! int || score < 0)) ||
        (kind == 'finish' && error != null && error is! String)) {
      throw const FormatException('Invalid MiniPlay bridge payload');
    }
    return BridgeEvent(kind: kind, game: value['game'] as String, taps: taps as int?, score: score as int?, error: error as String?);
  }
}
