import '../models/enums.dart';
import '../models/session.dart';
import 'respond_params.dart';

const _fallbackOptions = [
  DecisionOption(label: 'Allow', value: 'allow'),
  DecisionOption(label: 'Deny', value: 'deny', reject: true),
];

/// The server-built options of [ix], or Allow/Deny when it sent none.
List<DecisionOption> effectiveOptions(Interaction ix) =>
    ix.options.isNotEmpty ? ix.options : _fallbackOptions;

/// The respond `kind` for a permission or plan interaction.
String decisionKind(Interaction ix) {
  assert(
    ix.kind == InteractionKind.permission || ix.kind == InteractionKind.plan,
    'decisionKind needs a permission or plan interaction',
  );
  return ix.kind == InteractionKind.plan ? 'plan' : 'permission';
}

/// Respond params for choosing the non-reject option [o] in one tap.
Map<String, dynamic> oneTapRespond(Session s, DecisionOption o) {
  assert(!o.reject, 'reject options need a reason, not a one-tap respond');
  final ix = s.interaction!;
  return optionRespond(
    sessionId: s.id,
    kind: decisionKind(ix),
    value: o.value,
    requestId: ix.requestId,
  );
}
