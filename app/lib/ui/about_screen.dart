import 'package:flutter/material.dart';

import 'external_url.dart';
import 'responsive.dart';

const _privacyUrl = 'https://argus.muniftanjim.dev/privacy';
const _projectUrl = 'https://github.com/MunifTanjim/argus';

class AboutScreen extends StatelessWidget {
  const AboutScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('About')),
      body: CenteredBody(
        child: ListView(
          padding: const EdgeInsets.symmetric(vertical: 8),
          children: [
            ListTile(
              leading: const Icon(Icons.privacy_tip_outlined),
              title: const Text('Privacy policy'),
              subtitle: const Text('What Argus stores, and where'),
              trailing: const Icon(Icons.open_in_new, size: 18),
              onTap: () => openExternalUrl(_privacyUrl),
            ),
            ListTile(
              leading: const Icon(Icons.description_outlined),
              title: const Text('Open source licenses'),
              subtitle: const Text('Licenses of the bundled software'),
              trailing: const Icon(Icons.chevron_right),
              onTap: () => showLicensePage(
                context: context,
                applicationName: 'Argus',
                applicationLegalese: '© 2026 Munif Tanjim · MIT License',
              ),
            ),
            ListTile(
              leading: const Icon(Icons.code),
              title: const Text('Source code'),
              subtitle: const Text('github.com/MunifTanjim/argus'),
              trailing: const Icon(Icons.open_in_new, size: 18),
              onTap: () => openExternalUrl(_projectUrl),
            ),
          ],
        ),
      ),
    );
  }
}
