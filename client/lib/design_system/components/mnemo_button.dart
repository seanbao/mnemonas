import 'package:flutter/material.dart';

import '../tokens/mnemo_tokens.dart';

/// The primary MnemoNAS call-to-action with loading and disabled states.
class MnemoPrimaryButton extends StatelessWidget {
  const MnemoPrimaryButton({
    super.key,
    required this.label,
    required this.onPressed,
    this.icon,
    this.isLoading = false,
    this.expand = false,
    this.semanticLabel,
    this.focusNode,
    this.autofocus = false,
  });

  final String label;
  final VoidCallback? onPressed;
  final IconData? icon;
  final bool isLoading;
  final bool expand;
  final String? semanticLabel;
  final FocusNode? focusNode;
  final bool autofocus;

  @override
  Widget build(BuildContext context) {
    final ThemeData theme = Theme.of(context);
    final ColorScheme colors = theme.colorScheme;
    final bool enabled = onPressed != null && !isLoading;
    final bool reduceMotion =
        MediaQuery.maybeOf(context)?.disableAnimations ?? false;
    final BorderRadius radius = BorderRadius.circular(MnemoRadius.sm);
    final Gradient? gradient = enabled
        ? LinearGradient(
            begin: Alignment.topLeft,
            end: Alignment.bottomRight,
            colors: theme.brightness == Brightness.dark
                ? const <Color>[Color(0xFFAFC2FF), Color(0xFF8EA9F2)]
                : const <Color>[MnemoPalette.brand, Color(0xFF3F5BC0)],
          )
        : null;
    final Color foreground = enabled
        ? colors.onPrimary
        : colors.onSurface.withValues(alpha: 0.42);

    final ButtonStyle style = ButtonStyle(
      backgroundColor: const WidgetStatePropertyAll<Color>(Colors.transparent),
      foregroundColor: WidgetStatePropertyAll<Color>(foreground),
      overlayColor: WidgetStateProperty.resolveWith<Color?>((
        Set<WidgetState> states,
      ) {
        if (states.contains(WidgetState.pressed)) {
          return foreground.withValues(alpha: 0.16);
        }
        if (states.contains(WidgetState.hovered) ||
            states.contains(WidgetState.focused)) {
          return foreground.withValues(alpha: 0.1);
        }
        return null;
      }),
      minimumSize: const WidgetStatePropertyAll<Size>(
        Size(
          MnemoControlSize.minimumTouchTarget,
          MnemoControlSize.primaryButtonHeight,
        ),
      ),
      padding: const WidgetStatePropertyAll<EdgeInsetsGeometry>(
        EdgeInsets.symmetric(
          horizontal: MnemoSpacing.lg,
          vertical: MnemoSpacing.sm,
        ),
      ),
      elevation: const WidgetStatePropertyAll<double>(0),
      shadowColor: const WidgetStatePropertyAll<Color>(Colors.transparent),
      shape: WidgetStatePropertyAll<OutlinedBorder>(
        RoundedRectangleBorder(borderRadius: radius),
      ),
      textStyle: WidgetStatePropertyAll<TextStyle?>(
        theme.textTheme.labelLarge?.copyWith(fontWeight: FontWeight.w700),
      ),
    );

    final Widget indicator = SizedBox.square(
      dimension: 19,
      child: CircularProgressIndicator(strokeWidth: 2.2, color: foreground),
    );
    final Widget button = icon != null || isLoading
        ? FilledButton.icon(
            onPressed: enabled ? onPressed : null,
            focusNode: focusNode,
            autofocus: autofocus,
            style: style,
            icon: isLoading ? indicator : Icon(icon, size: 20),
            label: Text(label),
          )
        : FilledButton(
            onPressed: enabled ? onPressed : null,
            focusNode: focusNode,
            autofocus: autofocus,
            style: style,
            child: Text(label),
          );

    Widget result = AnimatedContainer(
      duration: reduceMotion ? Duration.zero : MnemoDuration.standard,
      curve: Curves.easeOutCubic,
      decoration: BoxDecoration(
        color: enabled ? null : colors.onSurface.withValues(alpha: 0.09),
        gradient: gradient,
        borderRadius: radius,
        boxShadow: enabled
            ? <BoxShadow>[
                BoxShadow(
                  color: colors.primary.withValues(alpha: 0.24),
                  blurRadius: 16,
                  offset: const Offset(0, 7),
                ),
              ]
            : const <BoxShadow>[],
      ),
      child: button,
    );
    if (expand) {
      result = SizedBox(width: double.infinity, child: result);
    }

    return Semantics(
      button: true,
      enabled: enabled,
      label: isLoading
          ? '${semanticLabel ?? label}，正在处理'
          : semanticLabel ?? label,
      onTap: enabled ? onPressed : null,
      child: ExcludeSemantics(child: result),
    );
  }
}
