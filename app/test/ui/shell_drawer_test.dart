import 'package:argus/ui/shell_drawer.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('no scope or no modal drawer: no menu button', (tester) async {
    Widget? seen = const SizedBox();
    await tester.pumpWidget(
      MaterialApp(
        home: Builder(
          builder: (context) {
            seen = shellMenuButton(context);
            return const SizedBox();
          },
        ),
      ),
    );
    expect(seen, isNull);

    await tester.pumpWidget(
      MaterialApp(
        home: ShellDrawerScope(
          openDrawer: null,
          child: Builder(
            builder: (context) {
              seen = shellMenuButton(context);
              return const SizedBox();
            },
          ),
        ),
      ),
    );
    expect(seen, isNull);
  });

  testWidgets('modal drawer: the button opens it', (tester) async {
    var opened = 0;
    await tester.pumpWidget(
      MaterialApp(
        home: ShellDrawerScope(
          openDrawer: () => opened++,
          child: Builder(
            builder: (context) =>
                Scaffold(appBar: AppBar(leading: shellMenuButton(context))),
          ),
        ),
      ),
    );
    await tester.tap(find.byIcon(Icons.menu));
    expect(opened, 1);
  });
}
