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

class InteractionBar extends StatelessWidget {
  const InteractionBar(
      {super.key, required this.interaction, required this.onRespond});

  final Interaction interaction;
  final VoidCallback onRespond;

  @override
  Widget build(BuildContext context) {
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
