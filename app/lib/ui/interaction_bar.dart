// app/lib/ui/interaction_bar.dart
import 'package:flutter/material.dart';

import '../models/enums.dart';
import '../models/session.dart';
import 'theme.dart';

String interactionLabel(Interaction ix) => switch (ix.kind) {
      InteractionKind.permission =>
        (ix.toolName ?? '').isNotEmpty ? 'Permission: ${ix.toolName}' : 'Permission',
      InteractionKind.plan => 'Plan review',
      InteractionKind.question => 'Question',
      InteractionKind.idle => 'Reply',
      InteractionKind.unknown => 'Respond',
    };

String respondElsewhereLabel(FrontendKind f) =>
    f == FrontendKind.vscode ? 'Respond in VSCode' : 'Respond in your terminal';

class InteractionBar extends StatelessWidget {
  const InteractionBar(
      {super.key,
      required this.interaction,
      required this.onRespond,
      this.informationalMessage});

  final Interaction interaction;
  final VoidCallback onRespond;
  final String? informationalMessage;

  @override
  Widget build(BuildContext context) {
    if (informationalMessage != null) {
      return Material(
        color: AppColors.awaitingSurface,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            mainAxisSize: MainAxisSize.min,
            children: [
              Text(informationalMessage!,
                  style: const TextStyle(
                      color: AppColors.secondary, fontWeight: FontWeight.bold)),
              const SizedBox(height: 2),
              const Text("argus can't send input to this session",
                  style: TextStyle(color: AppColors.secondary, fontSize: 12)),
            ],
          ),
        ),
      );
    }
    return Material(
      color: AppColors.awaitingSurface,
      child: InkWell(
        onTap: onRespond,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
          child: Row(
            children: [
              Expanded(
                child: Text(interactionLabel(interaction),
                    style: const TextStyle(
                        color: AppColors.secondary,
                        fontWeight: FontWeight.bold)),
              ),
              const Text('Respond',
                  style: TextStyle(color: AppColors.secondary)),
              const Icon(Icons.chevron_right, color: AppColors.secondary),
            ],
          ),
        ),
      ),
    );
  }
}
