import 'package:flutter/material.dart';
import 'package:mobile_scanner/mobile_scanner.dart';

import '../ui/titlebar.dart';
import 'pairing_uri.dart';

class ScanScreen extends StatefulWidget {
  const ScanScreen({super.key});

  @override
  State<ScanScreen> createState() => _ScanScreenState();
}

class _ScanScreenState extends State<ScanScreen> {
  bool _handled = false;

  void _onDetect(BarcodeCapture capture) {
    if (_handled) return;
    for (final b in capture.barcodes) {
      final c = parsePairingUri(b.rawValue ?? '');
      if (c != null) {
        _handled = true;
        Navigator.of(context).pop(c);
        return;
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: const ArgusAppBar(title: Text('Scan to connect')),
      body: MobileScanner(onDetect: _onDetect),
    );
  }
}
