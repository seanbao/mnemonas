import 'package:flutter/material.dart';

import '../tokens/mnemo_tokens.dart';

enum MnemoCardTone { standard, muted, elevated, brandTint }

/// A MnemoNAS surface with consistent padding, borders, focus, and elevation.
class MnemoCard extends StatelessWidget {
  const MnemoCard({
    super.key,
    required this.child,
    this.tone = MnemoCardTone.standard,
    this.padding = const EdgeInsets.all(MnemoSpacing.md),
    this.margin = EdgeInsets.zero,
    this.borderRadius,
    this.onTap,
    this.semanticLabel,
    this.tooltip,
    this.autofocus = false,
  });

  final Widget child;
  final MnemoCardTone tone;
  final EdgeInsetsGeometry padding;
  final EdgeInsetsGeometry margin;
  final BorderRadius? borderRadius;
  final VoidCallback? onTap;
  final String? semanticLabel;
  final String? tooltip;
  final bool autofocus;

  @override
  Widget build(BuildContext context) {
    final ThemeData theme = Theme.of(context);
    final ColorScheme colors = theme.colorScheme;
    final MnemoSemanticColors semantic = context.mnemoColors;
    final BorderRadius radius =
        borderRadius ?? BorderRadius.circular(MnemoRadius.md);
    final _CardVisuals visuals = switch (tone) {
      MnemoCardTone.standard => _CardVisuals(
        color: colors.surface,
        borderColor: colors.outlineVariant,
        shadow: const <BoxShadow>[],
      ),
      MnemoCardTone.muted => _CardVisuals(
        color: semantic.surfaceMuted,
        borderColor: colors.outlineVariant.withValues(alpha: 0.8),
        shadow: const <BoxShadow>[],
      ),
      MnemoCardTone.elevated => _CardVisuals(
        color: semantic.surfaceRaised,
        borderColor: colors.outlineVariant.withValues(alpha: 0.72),
        shadow: semantic.softShadow,
      ),
      MnemoCardTone.brandTint => _CardVisuals(
        color: colors.primaryContainer.withValues(
          alpha: theme.brightness == Brightness.dark ? 0.28 : 0.42,
        ),
        borderColor: colors.primary.withValues(alpha: 0.22),
        shadow: const <BoxShadow>[],
      ),
    };

    final Widget content = Padding(padding: padding, child: child);
    Widget surface = DecoratedBox(
      decoration: BoxDecoration(boxShadow: visuals.shadow),
      child: Material(
        color: Colors.transparent,
        clipBehavior: Clip.antiAlias,
        shape: RoundedRectangleBorder(
          borderRadius: radius,
          side: BorderSide(color: visuals.borderColor),
        ),
        child: Ink(
          decoration: BoxDecoration(color: visuals.color, borderRadius: radius),
          child: onTap == null
              ? content
              : InkWell(
                  onTap: onTap,
                  autofocus: autofocus,
                  borderRadius: radius,
                  mouseCursor: SystemMouseCursors.click,
                  child: content,
                ),
        ),
      ),
    );

    if (tooltip != null) {
      surface = Tooltip(message: tooltip!, child: surface);
    }

    return Padding(
      padding: margin,
      child: Semantics(
        container: true,
        button: onTap != null,
        enabled: onTap != null ? true : null,
        label: semanticLabel,
        child: surface,
      ),
    );
  }
}

class _CardVisuals {
  const _CardVisuals({
    required this.color,
    required this.borderColor,
    required this.shadow,
  });

  final Color color;
  final Color borderColor;
  final List<BoxShadow> shadow;
}
