/// Build-time PushPort constants, set with --dart-define (e.g.
/// ARGUS_PUSHPORT_APP_ID). appId is empty unless a build embeds it, leaving
/// PushPort unconfigured by default.
class PushPortConfig {
  static const String appId =
      String.fromEnvironment('ARGUS_PUSHPORT_APP_ID', defaultValue: '');
  static const String baseUrl = String.fromEnvironment('ARGUS_PUSHPORT_BASE_URL',
      defaultValue: 'https://pushport.muniftanjim.dev');

  static bool get isConfigured => appId.isNotEmpty && baseUrl.isNotEmpty;
}
