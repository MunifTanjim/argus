import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/session.dart';
import '../state/sessions.dart';
import 'session_composer.dart';
import 'theme.dart';

/// One item a session runs now. [open] builds the screen a tap pushes; null
/// when the item has nothing to open.
class RunningRow {
  const RunningRow({
    required this.title,
    this.subtitle,
    this.subtitleStyle,
    this.since,
    this.open,
  });

  final String title;
  final String? subtitle;
  final TextStyle? subtitleStyle;
  final DateTime? since;
  final WidgetBuilder? open;
}

/// Lists [rows] of the session with ticking elapsed times, and closes once
/// none are left. A picked row opens on [context]'s navigator, not the
/// sheet's.
Future<void> showRunningSheet(
  BuildContext context, {
  required String sessionId,
  required List<RunningRow> Function(Session) rows,
}) {
  final shell = Navigator.of(context);
  return showModalBottomSheet<void>(
    context: context,
    useRootNavigator: true,
    builder: (_) =>
        _RunningSheet(sessionId: sessionId, rows: rows, shell: shell),
  );
}

class _RunningSheet extends ConsumerStatefulWidget {
  const _RunningSheet({
    required this.sessionId,
    required this.rows,
    required this.shell,
  });

  final String sessionId;
  final List<RunningRow> Function(Session) rows;
  final NavigatorState shell;

  @override
  ConsumerState<_RunningSheet> createState() => _RunningSheetState();
}

class _RunningSheetState extends ConsumerState<_RunningSheet> {
  late final Timer _tick;

  @override
  void initState() {
    super.initState();
    _tick = Timer.periodic(const Duration(seconds: 1), (_) {
      if (mounted) setState(() {});
    });
  }

  @override
  void dispose() {
    _tick.cancel();
    super.dispose();
  }

  void _open(WidgetBuilder screen) {
    Navigator.of(context).pop();
    widget.shell.push(MaterialPageRoute(builder: screen));
  }

  @override
  Widget build(BuildContext context) {
    List<RunningRow> rowsOf(Session? s) =>
        s == null ? const [] : widget.rows(s);
    ref.listen(
      sessionsProvider.select((m) => rowsOf(m[widget.sessionId]).isEmpty),
      (_, empty) {
        // Not while closing: a pop then would take the screen above or below.
        if (empty && (ModalRoute.of(context)?.isCurrent ?? false)) {
          Navigator.of(context).pop();
        }
      },
    );
    final rows = rowsOf(
      ref.watch(sessionsProvider.select((m) => m[widget.sessionId])),
    );
    final now = DateTime.now();
    return SafeArea(
      child: ListView(
        shrinkWrap: true,
        padding: const EdgeInsets.only(top: 8, bottom: 8),
        children: [
          for (final r in rows)
            ListTile(
              title: Text(
                r.title,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
              subtitle: r.subtitle == null
                  ? null
                  : Text(
                      r.subtitle!,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: r.subtitleStyle,
                    ),
              trailing: r.since == null
                  ? null
                  : Text(
                      formatElapsed(now.difference(r.since!)),
                      style: mono.copyWith(color: AppColors.dim),
                    ),
              onTap: r.open == null ? null : () => _open(r.open!),
            ),
        ],
      ),
    );
  }
}
