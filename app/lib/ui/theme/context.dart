import 'package:flutter/material.dart';

import 'tokens.dart';

extension ArgusTokensContext on BuildContext {
  ArgusTokens get tokens =>
      Theme.of(this).extension<ArgusTokens>() ?? ArgusTokens.fallback;
}
