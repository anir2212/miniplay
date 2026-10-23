import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:webview_flutter/webview_flutter.dart';

import 'bridge_event.dart';

const configuredUrl = String.fromEnvironment('MINIPLAY_URL');
const configuredUser = String.fromEnvironment('MINIPLAY_USER', defaultValue: 'flutter_player');

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  SystemChrome.setPreferredOrientations([DeviceOrientation.portraitUp]);
  runApp(const MiniPlayHost());
}

class MiniPlayHost extends StatelessWidget {
  const MiniPlayHost({super.key});

  @override
  Widget build(BuildContext context) => MaterialApp(
        title: 'MiniPlay Host',
        theme: ThemeData.dark(useMaterial3: true),
        home: const GameScreen(),
      );
}

class GameScreen extends StatefulWidget {
  const GameScreen({super.key});

  @override
  State<GameScreen> createState() => _GameScreenState();
}

class _GameScreenState extends State<GameScreen> {
  late final WebViewController controller;
  late final Uri gameUri;
  String lastEvent = 'Loading games…';
  int progressCount = 0;

  @override
  void initState() {
    super.initState();
    final address = configuredUrl.isNotEmpty
        ? configuredUrl
        : Platform.isAndroid
            ? 'http://10.0.2.2:8080'
            : 'http://127.0.0.1:8080';
    gameUri = Uri.parse(address);
    controller = WebViewController()
      ..setJavaScriptMode(JavaScriptMode.unrestricted)
      ..setBackgroundColor(const Color(0xff10182b))
      ..addJavaScriptChannel('MiniPlayBridge', onMessageReceived: _handleMessage)
      ..setNavigationDelegate(NavigationDelegate(
        onNavigationRequest: (request) {
          final destination = Uri.tryParse(request.url);
          return destination?.origin == gameUri.origin
              ? NavigationDecision.navigate
              : NavigationDecision.prevent;
        },
        onPageFinished: (_) {
          final userJson = jsonEncode(configuredUser);
          controller.runJavaScript('window.MiniPlayHost.configure({userId:$userJson});');
        },
        onSslAuthError: (error) {
          // Never proceed past a certificate the platform does not trust.
          error.cancel();
          if (mounted) {
            setState(() => lastEvent = 'Could not load games: certificate rejected (${error.platform.description})');
          }
        },
        onWebResourceError: (error) {
          if (error.isForMainFrame == true && mounted) {
            setState(() => lastEvent = 'Could not load games: ${error.description}');
          }
        },
      ))
      ..loadRequest(gameUri);
  }

  void _handleMessage(JavaScriptMessage message) {
    try {
      final event = BridgeEvent.parse(message.message);
      if (!mounted) return;
      setState(() {
        switch (event.kind) {
          case 'start':
            lastEvent = 'Playing ${event.game}';
            progressCount = 0;
          case 'progress':
            progressCount = event.taps ?? 0;
          case 'finish':
            lastEvent = event.error == null
                ? '${event.game}: ${event.score} points'
                : '${event.game}: ${event.error}';
        }
      });
    } on FormatException {
      // Malformed page messages cannot alter host state.
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
        appBar: AppBar(title: const Text('MiniPlay')),
        body: SafeArea(
          child: Column(children: [
            Expanded(child: WebViewWidget(controller: controller)),
            Semantics(
              liveRegion: true,
              child: Padding(
                padding: const EdgeInsets.all(10),
                child: Text('$lastEvent · taps: $progressCount'),
              ),
            ),
          ]),
        ),
      );
}
