/// Build-time PushPort constants, set with --dart-define. appId is empty unless
/// a build embeds it, leaving PushPort unconfigured by default.
class PushPortConfig {
  static const String appId =
      String.fromEnvironment('PUSHPORT_APP_ID', defaultValue: '');
  static const String baseUrl = String.fromEnvironment('PUSHPORT_BASE_URL',
      defaultValue: 'https://pushport.muniftanjim.dev');

  static bool get isConfigured => appId.isNotEmpty && baseUrl.isNotEmpty;
}
