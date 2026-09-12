import 'package:url_launcher/url_launcher.dart';

/// Overridable in tests. Never throws — a bad link must not crash the render.
Future<void> Function(String url) openExternalUrl = _openExternalUrl;

Future<void> _openExternalUrl(String url) async {
  final uri = Uri.tryParse(url);
  if (uri == null) return;
  try {
    await launchUrl(uri, mode: LaunchMode.externalApplication);
  } catch (_) {
    // No handler for the scheme, or a platform error.
  }
}
