import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';

void main() {
  test('genesisFingerprintWords matches the CLI mnemonic for a known genesis', () {
    // Cross-implementation vector: `argus lock status` prints these 24 words under
    // genesis gen:063abe71...  The app must render the same words.
    final genesis = hexDecode(
        '063abe717f0919829c737fb9a994e13fba3070ff39d57a6c56fb783d68d4545d');

    final words = genesisFingerprintWords(genesis);

    expect(
      words,
      equals(
        'alert sting ordinary wrap muscle scout impact husband rifle erosion '
                'debate legal perfect debris woman deny kidney glance salute '
                'vacant story health fabric permit'
            .split(' '),
      ),
    );
  });
}
