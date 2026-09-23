import 'dart:convert';

import 'package:argus/data/openrouter.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

void main() {
  group('transcribe', () {
    test('sends the key, model and base64 audio, returns the text', () async {
      late http.Request sent;
      final client = OpenRouterClient(
        httpClient: MockClient((req) async {
          sent = req;
          return http.Response(jsonEncode({'text': ' hello world '}), 200);
        }),
      );

      final text = await client.transcribe(
        apiKey: 'sk-or-v1-abc',
        model: 'openai/whisper-large-v3-turbo',
        audio: [1, 2, 3],
      );

      expect(text, ' hello world ');
      expect(sent.url.path, '/api/v1/audio/transcriptions');
      expect(sent.headers['authorization'], 'Bearer sk-or-v1-abc');
      final body = jsonDecode(sent.body) as Map<String, dynamic>;
      expect(body['model'], 'openai/whisper-large-v3-turbo');
      expect(body['input_audio'], {
        'data': base64Encode([1, 2, 3]),
        'format': 'wav',
      });
    });

    test('a clip with no speech yields an empty string, not a throw', () async {
      final client = OpenRouterClient(
        httpClient: MockClient((_) async => http.Response('{}', 200)),
      );
      expect(
        await client.transcribe(apiKey: 'k', model: 'm', audio: const []),
        '',
      );
    });

    test('surfaces the OpenRouter error message', () async {
      final client = OpenRouterClient(
        httpClient: MockClient(
          (_) async => http.Response(
            jsonEncode({
              'error': {'message': 'No auth credentials found'}
            }),
            401,
          ),
        ),
      );
      await expectLater(
        client.transcribe(apiKey: '', model: 'm', audio: const [1]),
        throwsA(isA<OpenRouterException>().having(
          (e) => e.message,
          'message',
          'No auth credentials found',
        )),
      );
    });

    test('falls back to the status code when the body is not JSON', () async {
      final client = OpenRouterClient(
        httpClient: MockClient((_) async => http.Response('<html>502</html>', 502)),
      );
      await expectLater(
        client.transcribe(apiKey: 'k', model: 'm', audio: const [1]),
        throwsA(isA<OpenRouterException>()
            .having((e) => e.message, 'message', 'HTTP 502')),
      );
    });
  });

  group('transcriptionModels', () {
    OpenRouterClient clientFor(List<Map<String, dynamic>> data) =>
        OpenRouterClient(
          httpClient: MockClient((req) async {
            expect(
              req.url.queryParameters['output_modalities'],
              'transcription',
            );
            return http.Response(jsonEncode({'data': data}), 200);
          }),
        );

    test('sorts cheapest audio-minute first, token-billed models last',
        () async {
      final models = await clientFor([
        {
          'id': 'c/dear',
          'name': 'Dear',
          'pricing': {'prompt': '0.0001', 'completion': '0'},
        },
        {
          'id': 'd/token',
          'name': 'Token billed',
          'pricing': {'prompt': '0.0000025', 'completion': '0.00001'},
        },
        {
          'id': 'a/cheap',
          'name': 'Cheap',
          'pricing': {'prompt': '0.00000333', 'completion': '0'},
        },
      ]).transcriptionModels();

      expect(models.map((m) => m.id), ['a/cheap', 'c/dear', 'd/token']);
    });

    test('labels duration pricing per minute and token pricing per million',
        () async {
      final models = await clientFor([
        {
          'id': 'a/turbo',
          'name': 'Turbo',
          'pricing': {'prompt': '0.00000333', 'completion': '0'},
        },
        {
          'id': 'b/chirp',
          'name': 'Chirp',
          'pricing': {'prompt': '0.000266666666667', 'completion': '0'},
        },
        {
          'id': 'c/token',
          'name': 'Token',
          'pricing': {'prompt': '0.0000025', 'completion': '0.00001'},
        },
        {
          'id': 'd/free',
          'name': 'Free',
          'pricing': {'prompt': '0', 'completion': '0'},
        },
      ]).transcriptionModels();

      final label = {for (final m in models) m.id: m.priceLabel};
      expect(label['a/turbo'], r'$0.0002/min'); // 0.00000333 * 60
      expect(label['b/chirp'], r'$0.016/min');
      expect(label['c/token'], r'$2.50/M tok');
      expect(label['d/free'], 'free');
    });

    test('a model with no pricing gets an empty label and sorts last',
        () async {
      final models = await clientFor([
        {'id': 'n/three'}, // name and pricing both absent
        {
          'id': 'a/one',
          'name': 'One',
          'pricing': {'prompt': '0.0001', 'completion': '0'},
        },
      ]).transcriptionModels();

      expect(models.map((m) => m.id), ['a/one', 'n/three']);
      expect(models.last.name, 'n/three'); // name falls back to the id
      expect(models.last.priceLabel, isEmpty);
    });
  });
}
