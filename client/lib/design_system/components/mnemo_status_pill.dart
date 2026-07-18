import 'package:flutter/material.dart';

import '../tokens/mnemo_tokens.dart';

enum MnemoStatusTone { neutral, info, success, warning, danger }

/// A compact, semantic status indicator that remains legible in both themes.
class MnemoStatusPill extends StatelessWidget {
  const MnemoStatusPill({
    super.key,
    required this.label,
    this.tone = MnemoStatusTone.neutral,
    this.icon,
    this.showIcon = true,
    this.compact = false,
    this.liveRegion = false,
    this.tooltip,
  });

  final String label;
  final MnemoStatusTone tone;
  final IconData? icon;
  final bool showIcon;
  final bool compact;
  final bool liveRegion;
  final String? tooltip;

  @override
  Widget build(BuildContext context) {
    final ColorScheme colors = Theme.of(context).colorScheme;
    final MnemoSemanticColors semantic = context.mnemoColors;
    final _StatusVisuals visuals = switch (tone) {
      MnemoStatusTone.neutral => _StatusVisuals(
        foreground: colors.onSurfaceVariant,
        background: colors.surfaceContainerHighest,
        border: colors.outlineVariant,
        icon: Icons.circle,
      ),
      MnemoStatusTone.info => _StatusVisuals(
        foreground: semantic.onInfoContainer,
        background: semantic.infoContainer,
        border: semantic.info.withValues(alpha: 0.3),
        icon: Icons.info_rounded,
      ),
      MnemoStatusTone.success => _StatusVisuals(
        foreground: semantic.onSuccessContainer,
        background: semantic.successContainer,
        border: semantic.success.withValues(alpha: 0.3),
        icon: Icons.check_circle_rounded,
      ),
      MnemoStatusTone.warning => _StatusVisuals(
        foreground: semantic.onWarningContainer,
        background: semantic.warningContainer,
        border: semantic.warning.withValues(alpha: 0.32),
        icon: Icons.warning_amber_rounded,
      ),
      MnemoStatusTone.danger => _StatusVisuals(
        foreground: semantic.onDangerContainer,
        background: semantic.dangerContainer,
        border: colors.error.withValues(alpha: 0.3),
        icon: Icons.error_rounded,
      ),
    };

    Widget result = DecoratedBox(
      decoration: BoxDecoration(
        color: visuals.background,
        borderRadius: BorderRadius.circular(MnemoRadius.pill),
        border: Border.all(color: visuals.border),
      ),
      child: Padding(
        padding: EdgeInsets.symmetric(
          horizontal: compact ? MnemoSpacing.xs : MnemoSpacing.sm,
          vertical: compact ? MnemoSpacing.xxs : 6,
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: <Widget>[
            if (showIcon) ...<Widget>[
              Icon(
                icon ?? visuals.icon,
                size: compact ? 12 : 14,
                color: visuals.foreground,
              ),
              const SizedBox(width: 6),
            ],
            ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 240),
              child: Text(
                label,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: Theme.of(context).textTheme.labelSmall?.copyWith(
                  color: visuals.foreground,
                  fontWeight: FontWeight.w700,
                  height: 1.2,
                ),
              ),
            ),
          ],
        ),
      ),
    );

    if (tooltip != null) {
      result = Tooltip(message: tooltip!, child: result);
    }

    return Semantics(
      container: true,
      liveRegion: liveRegion,
      label: '状态：$label',
      child: ExcludeSemantics(child: result),
    );
  }
}

class _StatusVisuals {
  const _StatusVisuals({
    required this.foreground,
    required this.background,
    required this.border,
    required this.icon,
  });

  final Color foreground;
  final Color background;
  final Color border;
  final IconData icon;
}
