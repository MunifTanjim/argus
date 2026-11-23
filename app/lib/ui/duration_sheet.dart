import 'package:flutter/material.dart';

import 'adaptive_sheet.dart';

Future<String?> pickUntil(
  BuildContext context, {
  required String indefiniteLabel,
  required String indefinite,
}) {
  final options = <(String, Duration?)>[
    ('30 minutes', const Duration(minutes: 30)),
    ('1 hour', const Duration(hours: 1)),
    ('4 hours', const Duration(hours: 4)),
    ('8 hours', const Duration(hours: 8)),
    (indefiniteLabel, null),
  ];
  return showActionMenu<String>(
    context,
    items: [
      for (final (label, d) in options)
        ActionItem(
          label: label,
          value: d == null
              ? indefinite
              : DateTime.now().toUtc().add(d).toIso8601String(),
        ),
    ],
  );
}
