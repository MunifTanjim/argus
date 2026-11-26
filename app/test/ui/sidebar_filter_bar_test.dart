import 'package:argus/ui/sidebar_filter_bar.dart';
import 'package:argus/ui/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('each icon is as far from its side as from the top', (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        theme: buildArgusTheme(),
        home: Scaffold(
          body: Align(
            alignment: Alignment.bottomLeft,
            child: SizedBox(
              width: 300,
              child: SidebarFilterBar(
                controller: TextEditingController(text: 'abc'),
                hintText: 'Filter',
                onChanged: (_) {},
                options: const [],
                optionsActive: false,
                onSettings: () {},
              ),
            ),
          ),
        ),
      ),
    );
    final box = tester.getRect(find.byType(InputDecorator));
    final tune = tester.getRect(find.byIcon(Icons.tune));
    final text = tester.getRect(find.byType(EditableText));
    final clear = tester.getRect(find.byIcon(Icons.cancel));
    final inset = tune.top - box.top;
    expect(tune.left - box.left, inset);
    expect(box.right - clear.right, inset);
    expect(clear.top - box.top, inset);
    expect(text.left - tune.right, lessThanOrEqualTo(inset + 4));
  });
}
