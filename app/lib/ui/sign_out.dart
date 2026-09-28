import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../pairing/pairing_uri.dart';
import '../state/gateway.dart';

/// Pops the root navigator to its first route when the credentials are
/// cleared. Settings and its sub-screens are root routes above the home shell,
/// so without this they stay on top of the connection screen. Call from a
/// widget's build.
void popToRootOnSignOut(WidgetRef ref, GlobalKey<NavigatorState> navigatorKey) {
  ref.listen<GatewayCredentials?>(credentialsProvider, (prev, next) {
    if (prev != null && next == null) {
      navigatorKey.currentState?.popUntil((r) => r.isFirst);
    }
  });
}
