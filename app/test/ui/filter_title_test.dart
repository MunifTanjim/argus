import 'package:argus/ui/filter_title.dart';
import 'package:argus/ui/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

Widget _bar(String? query) => MaterialApp(
  theme: buildArgusTheme(),
  home: Scaffold(
    appBar: AppBar(
      leading: const BackButton(),
      title: FilterTitle(
        query: query,
        hint: 'Filter',
        onChanged: (_) {},
        child: const Text('History'),
      ),
      actions: [
        FilterButton(
          query: query,
          tooltip: 'Filter',
          onOpen: () {},
          onClose: () {},
        ),
      ],
    ),
  ),
);

void main() {
  testWidgets('the open field lines up with the title it replaces', (
    tester,
  ) async {
    await tester.pumpWidget(_bar(null));
    final title = tester.getRect(find.text('History'));
    final search = tester.getRect(find.byIcon(Icons.search));
    final titleStyle = tester
        .widget<RichText>(
          find.descendant(
            of: find.text('History'),
            matching: find.byType(RichText),
          ),
        )
        .text
        .style;

    await tester.pumpWidget(_bar(''));
    await tester.pump();
    final field = tester.getRect(find.byType(EditableText));
    final style = tester.widget<EditableText>(find.byType(EditableText)).style;

    expect(field.left, title.left);
    expect(field.center.dy, closeTo(title.center.dy, 0.5));
    expect(style.fontFamily, titleStyle?.fontFamily);
    expect(style.fontSize, titleStyle?.fontSize);
    expect(style.fontWeight, titleStyle?.fontWeight);
    // The close action takes the search action's place.
    expect(tester.getRect(find.byIcon(Icons.close)), search);
  });
}
