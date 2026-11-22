/// An agent action's warning classes in words; unknown classes are left out.
List<String> classWarnings(Iterable<String> classes) {
  const words = {
    'irreversible': "can't be undone",
    'security': 'changes security settings',
    'outward': 'sends data out of this machine',
  };
  return [
    for (final c in classes)
      if (words[c] != null) words[c]!,
  ];
}

/// Who started the agent work that asks, when it was not the user.
String startedByLabel(String? by) => switch (by) {
  null || '' || 'you' => '',
  'check-back' => 'started by a check-back',
  'event' => 'started by an event',
  'handoff' => 'started by a hand-off',
  'routine' => 'started by a routine',
  _ => 'started by $by',
};
