import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/enums.dart';

void main() {
  test('status wire mapping', () {
    expect(statusFromWire('awaiting_input'), SessionStatus.awaitingInput);
    expect(statusFromWire('working'), SessionStatus.working);
    expect(statusFromWire('dead'), SessionStatus.dead);
    expect(statusFromWire('bogus'), SessionStatus.unknown);
    expect(statusFromWire(null), SessionStatus.unknown);
  });

  test('source wire mapping', () {
    expect(sourceFromWire('spawned'), SessionSource.spawned);
    expect(sourceFromWire('hooked'), SessionSource.hooked);
  });

  test('tmux server wire mapping', () {
    expect(tmuxServerFromWire('default'), TmuxServerKind.default_);
    expect(tmuxServerFromWire('argus'), TmuxServerKind.argus);
  });

  test('interaction kind wire mapping', () {
    expect(interactionKindFromWire('permission'), InteractionKind.permission);
    expect(interactionKindFromWire('question'), InteractionKind.question);
    expect(interactionKindFromWire('plan'), InteractionKind.plan);
    expect(interactionKindFromWire('idle'), InteractionKind.idle);
  });
}
