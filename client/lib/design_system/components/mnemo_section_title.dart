import 'package:flutter/material.dart';

import '../tokens/mnemo_tokens.dart';

/// A responsive title block for sections and grouped settings.
class MnemoSectionTitle extends StatelessWidget {
  const MnemoSectionTitle({
    super.key,
    required this.title,
    this.description,
    this.eyebrow,
    this.leading,
    this.action,
    this.padding = EdgeInsets.zero,
  });

  final String title;
  final String? description;
  final String? eyebrow;
  final Widget? leading;
  final Widget? action;
  final EdgeInsetsGeometry padding;

  @override
  Widget build(BuildContext context) {
    final ThemeData theme = Theme.of(context);
    final ColorScheme colors = theme.colorScheme;
    final Widget heading = Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: <Widget>[
        if (leading != null) ...<Widget>[
          DecoratedBox(
            decoration: BoxDecoration(
              color: colors.primaryContainer,
              borderRadius: BorderRadius.circular(MnemoRadius.sm),
            ),
            child: Padding(
              padding: const EdgeInsets.all(MnemoSpacing.xs),
              child: IconTheme(
                data: IconThemeData(color: colors.onPrimaryContainer, size: 22),
                child: leading!,
              ),
            ),
          ),
          const SizedBox(width: MnemoSpacing.sm),
        ],
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: <Widget>[
              if (eyebrow != null) ...<Widget>[
                Text(
                  eyebrow!.toUpperCase(),
                  style: theme.textTheme.labelSmall?.copyWith(
                    color: colors.primary,
                    fontWeight: FontWeight.w700,
                    letterSpacing: 0.8,
                  ),
                ),
                const SizedBox(height: MnemoSpacing.xxs),
              ],
              Semantics(
                header: true,
                child: Text(title, style: theme.textTheme.titleLarge),
              ),
              if (description != null) ...<Widget>[
                const SizedBox(height: MnemoSpacing.xxs),
                Text(
                  description!,
                  style: theme.textTheme.bodyMedium?.copyWith(
                    color: colors.onSurfaceVariant,
                  ),
                ),
              ],
            ],
          ),
        ),
      ],
    );

    return Padding(
      padding: padding,
      child: LayoutBuilder(
        builder: (BuildContext context, BoxConstraints constraints) {
          final bool useStackedLayout =
              constraints.maxWidth < 520 ||
              MediaQuery.textScalerOf(context).scale(16) > 21;
          if (action == null) {
            return heading;
          }
          if (useStackedLayout) {
            return Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: <Widget>[
                heading,
                const SizedBox(height: MnemoSpacing.sm),
                Align(alignment: Alignment.centerLeft, child: action),
              ],
            );
          }
          return Row(
            crossAxisAlignment: CrossAxisAlignment.center,
            children: <Widget>[
              Expanded(child: heading),
              const SizedBox(width: MnemoSpacing.lg),
              action!,
            ],
          );
        },
      ),
    );
  }
}
