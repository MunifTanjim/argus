import 'package:argus/models/project_sources.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/projects_api.dart';
import 'package:argus/ui/branch_picker_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('filters, marks current and origin, and returns the pick', (
    tester,
  ) async {
    final c = ProviderContainer(
      overrides: [
        gatewayProvider.overrideWithValue(null),
        projectBranchesProvider('A:p1').overrideWith(
          (ref) async => const [
            BranchInfo(name: 'main', local: true),
            BranchInfo(name: 'dev', local: true, checkedOut: true),
            BranchInfo(name: 'feat/remote', remote: true),
          ],
        ),
      ],
    );
    addTearDown(c.dispose);
    String? picked;
    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: c,
        child: MaterialApp(
          home: Builder(
            builder: (ctx) => TextButton(
              onPressed: () async => picked = await pickBranch(
                Navigator.of(ctx),
                projectId: 'A:p1',
                title: 'Target for reg',
                current: 'main',
              ),
              child: const Text('go'),
            ),
          ),
        ),
      ),
    );
    await tester.tap(find.text('go'));
    await tester.pumpAndSettle();
    expect(find.text('Target for reg'), findsOneWidget);
    expect(find.text('(current)'), findsOneWidget);
    expect(find.text('origin'), findsOneWidget);
    // The picker does not disable in-use branches.
    await tester.enterText(find.byKey(const Key('branch-filter')), 'DE');
    await tester.pump();
    expect(find.text('main'), findsNothing);
    await tester.tap(find.text('dev'));
    await tester.pumpAndSettle();
    expect(picked, 'dev');
  });
}
