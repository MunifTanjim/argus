import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/result.dart';
import '../data/session_repository.dart';
import '../models/session.dart';
import '../state/live_screen_view_model.dart';
import 'ansi_text.dart';

class LiveScreenScreen extends ConsumerStatefulWidget {
  const LiveScreenScreen({super.key, required this.session});

  final Session session;

  @override
  ConsumerState<LiveScreenScreen> createState() => _LiveScreenScreenState();
}

class _LiveScreenScreenState extends ConsumerState<LiveScreenScreen> {
  late final LiveScreenViewModel _vm;
  String? _screen;
  Timer? _timer;
  final TextEditingController _textController = TextEditingController();

  @override
  void initState() {
    super.initState();
    _vm = LiveScreenViewModel(
        ref.read(sessionRepositoryProvider), widget.session.id);
    _vm.capture.addListener(_onCapture);
    // Immediate capture on init.
    _vm.capture.execute();
    // Periodic polling every 750 ms; the command's running-guard drops ticks
    // that would overlap an in-flight capture.
    _timer = Timer.periodic(const Duration(milliseconds: 750), (_) {
      _vm.capture.execute();
    });
  }

  void _onCapture() {
    if (!mounted) return;
    // A failed capture is a transient gap (reconnect / dead pane); the next
    // tick retries, so errors are intentionally dropped here.
    if (_vm.capture.result case Ok(:final value)) {
      setState(() => _screen = value);
    }
  }

  @override
  void dispose() {
    _timer?.cancel();
    _vm.capture.removeListener(_onCapture);
    _vm.dispose();
    _textController.dispose();
    super.dispose();
  }

  void _report(Result<void>? result) {
    if (!mounted) return;
    if (result case Error(:final error)) {
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Failed: $error')));
    }
  }

  Future<void> _sendKeys(List<String> keys) async {
    await _vm.sendKeys.execute(keys);
    _report(_vm.sendKeys.result);
  }

  Future<void> _sendRaw(String text) async {
    _textController.clear();
    await _vm.sendRaw.execute(text);
    _report(_vm.sendRaw.result);
  }

  @override
  Widget build(BuildContext context) {
    final title = widget.session.displayTitle;

    return Scaffold(
      appBar: AppBar(title: Text(title)),
      backgroundColor: Colors.black,
      body: Column(
        children: [
          Expanded(
            child: SingleChildScrollView(
              padding: const EdgeInsets.all(8),
              child: _screen == null
                  ? const SizedBox.shrink()
                  : AnsiText(_screen!),
            ),
          ),
          _InputBar(
            controller: _textController,
            onSend: _sendRaw,
            onKey: _sendKeys,
          ),
        ],
      ),
    );
  }
}

class _InputBar extends StatelessWidget {
  const _InputBar({
    required this.controller,
    required this.onSend,
    required this.onKey,
  });

  final TextEditingController controller;
  final Future<void> Function(String text) onSend;
  final Future<void> Function(List<String> keys) onKey;

  static const _quickKeys = [
    ('↵', 'Enter'),
    ('Esc', 'Escape'),
    ('^C', 'C-c'),
    ('Tab', 'Tab'),
    ('↑', 'Up'),
    ('↓', 'Down'),
  ];

  @override
  Widget build(BuildContext context) {
    return SafeArea(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          // Quick-key buttons row.
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
            child: Wrap(
              spacing: 4,
              runSpacing: 4,
              children: [
                for (final (label, key) in _quickKeys)
                  OutlinedButton(
                    style: OutlinedButton.styleFrom(
                      padding: const EdgeInsets.symmetric(
                          horizontal: 8, vertical: 4),
                      minimumSize: const Size(0, 32),
                      tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                    ),
                    onPressed: () => onKey([key]),
                    child: Text(label,
                        style: const TextStyle(
                            fontFamily: 'monospace', fontSize: 12)),
                  ),
              ],
            ),
          ),
          // Text input row.
          Padding(
            padding: const EdgeInsets.fromLTRB(8, 0, 8, 8),
            child: Row(
              children: [
                Expanded(
                  child: TextField(
                    controller: controller,
                    style: const TextStyle(fontFamily: 'monospace'),
                    decoration: const InputDecoration(
                      isDense: true,
                      contentPadding:
                          EdgeInsets.symmetric(horizontal: 8, vertical: 8),
                      border: OutlineInputBorder(),
                    ),
                  ),
                ),
                const SizedBox(width: 8),
                ElevatedButton(
                  onPressed: () {
                    final text = controller.text;
                    if (text.isNotEmpty) onSend(text);
                  },
                  child: const Text('Send'),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}
