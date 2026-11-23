import 'package:flutter/foundation.dart'
    show defaultTargetPlatform, TargetPlatform;

bool get isMobile =>
    defaultTargetPlatform == TargetPlatform.android ||
    defaultTargetPlatform == TargetPlatform.iOS;

/// Push delivery (UnifiedPush, FCM, APNs) is wired for the mobile apps only.
bool get supportsPush => isMobile;

/// mobile_scanner has no Linux implementation.
bool get supportsQrScan =>
    isMobile || defaultTargetPlatform == TargetPlatform.macOS;

/// stts has no Linux implementation.
bool get supportsSystemSpeech =>
    isMobile || defaultTargetPlatform == TargetPlatform.macOS;
