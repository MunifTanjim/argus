import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../state/appearance.dart';
import 'responsive.dart';
import 'theme.dart';
import 'top_bar.dart';

const _paletteNames = {
  ArgusPaletteId.gruvbox: 'Gruvbox',
  ArgusPaletteId.catppuccin: 'Catppuccin',
  ArgusPaletteId.tokyoNight: 'Tokyo Night',
  ArgusPaletteId.nord: 'Nord',
  ArgusPaletteId.solarized: 'Solarized',
  ArgusPaletteId.kanagawa: 'Kanagawa',
};

class AppearanceScreen extends ConsumerWidget {
  const AppearanceScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final prefs = ref.watch(appearancePrefsProvider);
    final c = ref.read(appearancePrefsProvider.notifier);
    final brightness = Theme.of(context).brightness;
    final palette = paletteFor(prefs.palette, brightness);

    return Scaffold(
      appBar: const TopBar(title: Text('Appearance')),
      body: SafeArea(
        top: false,
        child: CenteredBody(
          child: ListView(
            padding: const EdgeInsets.symmetric(vertical: 8),
            children: [
              const _Heading('Theme'),
              const _Label('Palette'),
              for (final id in ArgusPaletteId.values)
                _PaletteTile(
                  name: _paletteNames[id]!,
                  palette: paletteFor(id, brightness),
                  selected: id == prefs.palette,
                  onTap: () => c.setPalette(id),
                ),
              const _Label('Mode'),
              _Segments<ThemeMode>(
                value: prefs.mode,
                onChanged: c.setMode,
                options: const {
                  ThemeMode.system: 'System',
                  ThemeMode.dark: 'Dark',
                  ThemeMode.light: 'Light',
                },
              ),
              const _Label('Accent'),
              _AccentRow(
                palette: palette,
                selected: prefs.accent,
                onChanged: c.setAccent,
              ),
              const _Heading('Layout'),
              _TextScaleSlider(
                value: prefs.textScale,
                onChanged: c.setTextScale,
              ),
              const _Label('Corner radius'),
              _Segments<CornerRadius>(
                value: prefs.radius,
                onChanged: c.setRadius,
                options: const {
                  CornerRadius.sharp: 'Sharp',
                  CornerRadius.medium: 'Medium',
                  CornerRadius.rounded: 'Rounded',
                },
              ),
              const _Label('Status style'),
              _Segments<StatusStyle>(
                value: prefs.statusStyle,
                onChanged: c.setStatusStyle,
                options: const {
                  StatusStyle.dot: 'Dot',
                  StatusStyle.stripe: 'Stripe',
                  StatusStyle.filled: 'Filled',
                },
              ),
              const _Heading('Content'),
              const _Label('Interface font'),
              _Segments<InterfaceFont>(
                value: prefs.font,
                onChanged: c.setFont,
                options: const {
                  InterfaceFont.sans: 'Sans',
                  InterfaceFont.mono: 'Mono',
                },
              ),
              SwitchListTile(
                value: prefs.verboseTranscript,
                onChanged: c.setVerboseTranscript,
                title: const Text('Verbose transcript'),
                subtitle: const Text(
                  'Expand thinking and tool runs by default',
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _Heading extends StatelessWidget {
  const _Heading(this.text);
  final String text;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.fromLTRB(16, 24, 16, 0),
    child: Text(
      text,
      style: Theme.of(context).textTheme.titleMedium?.copyWith(
        color: Theme.of(context).colorScheme.primary,
      ),
    ),
  );
}

class _Label extends StatelessWidget {
  const _Label(this.text);
  final String text;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.fromLTRB(16, 12, 16, 6),
    child: Text(text, style: Theme.of(context).textTheme.labelLarge),
  );
}

/// Shows the size while dragging and applies it on release, so the layout
/// does not resize under the finger.
class _TextScaleSlider extends StatefulWidget {
  const _TextScaleSlider({required this.value, required this.onChanged});
  final double value;
  final ValueChanged<double> onChanged;

  @override
  State<_TextScaleSlider> createState() => _TextScaleSliderState();
}

class _TextScaleSliderState extends State<_TextScaleSlider> {
  double? _drag;

  @override
  Widget build(BuildContext context) {
    final v = _drag ?? widget.value.clamp(textScaleMin, textScaleMax);
    final style = Theme.of(context).textTheme.labelLarge;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 0),
          child: Row(
            children: [
              Expanded(child: Text('Text size', style: style)),
              Text('${(v * 100).round()}%', style: style),
            ],
          ),
        ),
        Slider(
          key: const Key('text-scale-slider'),
          min: textScaleMin,
          max: textScaleMax,
          divisions: textScaleDivisions,
          value: v,
          onChanged: (x) => setState(() => _drag = snapTextScale(x)),
          onChangeEnd: (x) {
            setState(() => _drag = null);
            widget.onChanged(snapTextScale(x));
          },
        ),
      ],
    );
  }
}

class _Segments<T> extends StatelessWidget {
  const _Segments({
    required this.value,
    required this.onChanged,
    required this.options,
  });
  final T value;
  final ValueChanged<T> onChanged;
  final Map<T, String> options;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.symmetric(horizontal: 16),
    child: SegmentedButton<T>(
      showSelectedIcon: false,
      segments: [
        for (final e in options.entries)
          ButtonSegment(value: e.key, label: Text(e.value)),
      ],
      selected: {value},
      onSelectionChanged: (s) => onChanged(s.first),
    ),
  );
}

class _PaletteTile extends StatelessWidget {
  const _PaletteTile({
    required this.name,
    required this.palette,
    required this.selected,
    required this.onTap,
  });
  final String name;
  final ArgusPalette palette;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final colors = [
      palette.bg0,
      palette.red,
      palette.yellow,
      palette.green,
      palette.blue,
    ];
    return ListTile(
      dense: true,
      selected: selected,
      onTap: onTap,
      title: Text(name),
      leading: Icon(
        selected ? Icons.radio_button_checked : Icons.radio_button_off,
      ),
      trailing: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          for (final col in colors)
            Container(
              width: 18,
              height: 18,
              margin: const EdgeInsets.only(left: 4),
              decoration: BoxDecoration(
                color: col,
                shape: BoxShape.circle,
                border: Border.all(color: palette.dim),
              ),
            ),
        ],
      ),
    );
  }
}

class _AccentRow extends StatelessWidget {
  const _AccentRow({
    required this.palette,
    required this.selected,
    required this.onChanged,
  });
  final ArgusPalette palette;
  final AccentChoice selected;
  final ValueChanged<AccentChoice> onChanged;

  @override
  Widget build(BuildContext context) {
    final outline = Theme.of(context).colorScheme.onSurface;
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16),
      child: Wrap(
        spacing: 10,
        runSpacing: 10,
        children: [
          for (final a in AccentChoice.values)
            Semantics(
              button: true,
              selected: a == selected,
              label: a == AccentChoice.paletteDefault
                  ? 'Palette default'
                  : a.name,
              child: GestureDetector(
                key: Key('accent-${a.name}'),
                onTap: () => onChanged(a),
                child: Container(
                  width: 32,
                  height: 32,
                  decoration: BoxDecoration(
                    shape: BoxShape.circle,
                    color: accentColor(palette, a) ?? palette.bg2,
                    border: Border.all(
                      color: a == selected ? outline : palette.dim,
                      width: a == selected ? 3 : 1,
                    ),
                  ),
                  child: a == AccentChoice.paletteDefault
                      ? Icon(Icons.auto_awesome, size: 16, color: palette.fg0)
                      : null,
                ),
              ),
            ),
        ],
      ),
    );
  }
}
