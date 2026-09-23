import 'dart:convert';

import 'package:http/http.dart' as http;

/// OpenRouter's REST base. Voice input is bring-your-own-key, so the app calls
/// OpenRouter straight from the device instead of proxying through the gateway
/// — the gateway never sees the key or the audio.
const openRouterBase = 'https://openrouter.ai/api/v1';

/// Upstream transcription providers cut off at 60s; leave slack for uploading
/// a long recording over a phone connection.
const openRouterTimeout = Duration(seconds: 90);

/// A speech-to-text model offered by OpenRouter.
///
/// OpenRouter bills transcription two ways. Most models charge per second of
/// audio; a few newer OpenAI ones charge per token like a text model. The two
/// are not comparable, so they are kept in separate fields rather than flattened
/// into one number.
class TranscriptionModel {
  const TranscriptionModel(
    this.id,
    this.name, {
    this.perAudioSecond,
    this.perInputToken,
  });

  final String id;
  final String name;

  /// USD per second of audio. Null when the model bills by token.
  final double? perAudioSecond;

  /// USD per input token. Null when the model bills by audio duration.
  final double? perInputToken;

  /// A short price tag for the picker, e.g. `$0.0002/min`. Empty when
  /// OpenRouter reports no price for the model.
  String get priceLabel {
    final second = perAudioSecond;
    if (second != null) return second == 0 ? 'free' : '\$${_usd(second * 60)}/min';
    final token = perInputToken;
    if (token != null) return '\$${_usd(token * 1000000)}/M tok';
    return '';
  }

  /// Cheapest audio-minute first. Token-billed models sort last: their cost
  /// depends on how much you say, not how long you hold the button, so there is
  /// no honest way to rank them against a per-minute rate.
  static int byPrice(TranscriptionModel a, TranscriptionModel b) {
    final x = a.perAudioSecond, y = b.perAudioSecond;
    if (x == null && y == null) return a.name.compareTo(b.name);
    if (x == null) return 1;
    if (y == null) return -1;
    return x == y ? a.name.compareTo(b.name) : x.compareTo(y);
  }
}

/// Scales the precision to the value: two decimals says nothing at
/// $0.0002/min, and four are noise at $6.00/min.
String _usd(double v) => v >= 1
    ? v.toStringAsFixed(2)
    : v >= 0.01
        ? v.toStringAsFixed(3)
        : v.toStringAsFixed(4);

class OpenRouterException implements Exception {
  OpenRouterException(this.message);
  final String message;

  @override
  String toString() => message;
}

class OpenRouterClient {
  OpenRouterClient({http.Client? httpClient, this._timeout = openRouterTimeout})
      : _http = httpClient ?? http.Client();

  final http.Client _http;
  final Duration _timeout;

  /// The speech-to-text catalog, cheapest first. Fetched rather than hardcoded:
  /// the list turns over often and this endpoint needs no key.
  Future<List<TranscriptionModel>> transcriptionModels() async {
    final res = await _http
        .get(Uri.parse('$openRouterBase/models?output_modalities=transcription'))
        .timeout(_timeout);
    if (res.statusCode != 200) {
      throw OpenRouterException(_errorMessage(res.statusCode, res.body));
    }
    final body = jsonDecode(res.body) as Map<String, dynamic>;
    final models = [
      for (final m in (body['data'] as List).cast<Map<String, dynamic>>())
        if (m['id'] is String) _model(m),
    ];
    return models..sort(TranscriptionModel.byPrice);
  }

  /// Transcribes [audio] and returns the text, empty when the clip held no
  /// speech. [format] is the container, e.g. `wav`.
  ///
  /// Sends base64 JSON rather than multipart: OpenRouter recommends it and it
  /// saves building a multipart body for one call.
  Future<String> transcribe({
    required String apiKey,
    required String model,
    required List<int> audio,
    String format = 'wav',
  }) async {
    final res = await _http
        .post(
          Uri.parse('$openRouterBase/audio/transcriptions'),
          headers: {
            'authorization': 'Bearer $apiKey',
            'content-type': 'application/json',
          },
          body: jsonEncode({
            'model': model,
            'input_audio': {'data': base64Encode(audio), 'format': format},
          }),
        )
        .timeout(_timeout);
    if (res.statusCode != 200) {
      throw OpenRouterException(_errorMessage(res.statusCode, res.body));
    }
    final body = jsonDecode(res.body) as Map<String, dynamic>;
    return body['text'] as String? ?? '';
  }
}

/// Builds a model from one `/models` entry.
///
/// ponytail: a non-zero completion price is the only signal in the payload that
/// a model bills per token rather than per audio second — the API exposes no
/// unit field. Revisit if OpenRouter adds one.
TranscriptionModel _model(Map<String, dynamic> m) {
  final pricing = m['pricing'] as Map<String, dynamic>? ?? const {};
  final prompt = double.tryParse('${pricing['prompt']}');
  final completion = double.tryParse('${pricing['completion']}') ?? 0;
  final tokenBilled = completion > 0;
  return TranscriptionModel(
    m['id'] as String,
    m['name'] as String? ?? m['id'] as String,
    perAudioSecond: tokenBilled ? null : prompt,
    perInputToken: tokenBilled ? prompt : null,
  );
}

/// OpenRouter reports failures as `{"error":{"message":…}}`. Falls back to the
/// status code when the body is not that shape — a proxy 502 returns HTML.
String _errorMessage(int status, String body) {
  try {
    final err = (jsonDecode(body) as Map<String, dynamic>)['error'];
    if (err is Map && err['message'] is String) return err['message'] as String;
  } catch (_) {
    // Not JSON; fall through to the status code.
  }
  return 'HTTP $status';
}
