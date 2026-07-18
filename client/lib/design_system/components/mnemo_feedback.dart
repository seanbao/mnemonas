import 'dart:async';

import 'package:flutter/material.dart';

import '../tokens/mnemo_tokens.dart';

/// A calm empty state for files, search results, and first-run experiences.
class MnemoEmptyState extends StatelessWidget {
  const MnemoEmptyState({
    super.key,
    required this.title,
    required this.message,
    this.icon = Icons.folder_open_rounded,
    this.illustration,
    this.primaryAction,
    this.secondaryAction,
    this.maxWidth = 460,
  });

  final String title;
  final String message;
  final IconData icon;
  final Widget? illustration;
  final Widget? primaryAction;
  final Widget? secondaryAction;
  final double maxWidth;

  @override
  Widget build(BuildContext context) {
    final ColorScheme colors = Theme.of(context).colorScheme;
    return Semantics(
      container: true,
      child: Center(
        child: ConstrainedBox(
          constraints: BoxConstraints(maxWidth: maxWidth),
          child: Padding(
            padding: const EdgeInsets.symmetric(
              horizontal: MnemoSpacing.lg,
              vertical: MnemoSpacing.xxl,
            ),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: <Widget>[
                illustration ??
                    DecoratedBox(
                      decoration: BoxDecoration(
                        color: colors.primaryContainer.withValues(alpha: 0.72),
                        shape: BoxShape.circle,
                      ),
                      child: Padding(
                        padding: const EdgeInsets.all(MnemoSpacing.lg),
                        child: Icon(
                          icon,
                          size: 36,
                          color: colors.onPrimaryContainer,
                        ),
                      ),
                    ),
                const SizedBox(height: MnemoSpacing.lg),
                Semantics(
                  header: true,
                  child: Text(
                    title,
                    textAlign: TextAlign.center,
                    style: Theme.of(context).textTheme.titleLarge,
                  ),
                ),
                const SizedBox(height: MnemoSpacing.xs),
                Text(
                  message,
                  textAlign: TextAlign.center,
                  style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                    color: colors.onSurfaceVariant,
                  ),
                ),
                if (primaryAction != null || secondaryAction != null) ...[
                  const SizedBox(height: MnemoSpacing.xl),
                  Wrap(
                    alignment: WrapAlignment.center,
                    crossAxisAlignment: WrapCrossAlignment.center,
                    spacing: MnemoSpacing.sm,
                    runSpacing: MnemoSpacing.sm,
                    children: <Widget>[?primaryAction, ?secondaryAction],
                  ),
                ],
              ],
            ),
          ),
        ),
      ),
    );
  }
}

/// An accessible inline error with optional recovery and dismissal actions.
class MnemoErrorNotice extends StatelessWidget {
  const MnemoErrorNotice({
    super.key,
    required this.title,
    required this.message,
    this.onRetry,
    this.retryLabel = '重试',
    this.onDismiss,
    this.dismissTooltip = '关闭提示',
  });

  final String title;
  final String message;
  final VoidCallback? onRetry;
  final String retryLabel;
  final VoidCallback? onDismiss;
  final String dismissTooltip;

  @override
  Widget build(BuildContext context) {
    final ThemeData theme = Theme.of(context);
    final ColorScheme colors = theme.colorScheme;
    final MnemoSemanticColors semantic = context.mnemoColors;

    return Semantics(
      container: true,
      liveRegion: true,
      label: '$title。$message',
      explicitChildNodes: true,
      child: Material(
        color: semantic.dangerContainer,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(MnemoRadius.md),
          side: BorderSide(color: colors.error.withValues(alpha: 0.28)),
        ),
        child: Padding(
          padding: const EdgeInsets.all(MnemoSpacing.md),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: <Widget>[
              ExcludeSemantics(
                child: DecoratedBox(
                  decoration: BoxDecoration(
                    color: colors.error.withValues(alpha: 0.12),
                    shape: BoxShape.circle,
                  ),
                  child: Padding(
                    padding: const EdgeInsets.all(MnemoSpacing.xs),
                    child: Icon(
                      Icons.error_outline_rounded,
                      size: 22,
                      color: semantic.onDangerContainer,
                    ),
                  ),
                ),
              ),
              const SizedBox(width: MnemoSpacing.sm),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: <Widget>[
                    ExcludeSemantics(
                      child: Text(
                        title,
                        style: theme.textTheme.titleSmall?.copyWith(
                          color: semantic.onDangerContainer,
                        ),
                      ),
                    ),
                    const SizedBox(height: MnemoSpacing.xxs),
                    ExcludeSemantics(
                      child: Text(
                        message,
                        style: theme.textTheme.bodyMedium?.copyWith(
                          color: semantic.onDangerContainer.withValues(
                            alpha: 0.88,
                          ),
                        ),
                      ),
                    ),
                    if (onRetry != null) ...<Widget>[
                      const SizedBox(height: MnemoSpacing.xs),
                      TextButton.icon(
                        onPressed: onRetry,
                        style: TextButton.styleFrom(
                          foregroundColor: semantic.onDangerContainer,
                          minimumSize: const Size(48, 44),
                          padding: const EdgeInsets.symmetric(
                            horizontal: MnemoSpacing.sm,
                          ),
                        ),
                        icon: const Icon(Icons.refresh_rounded, size: 18),
                        label: Text(retryLabel),
                      ),
                    ],
                  ],
                ),
              ),
              if (onDismiss != null) ...<Widget>[
                const SizedBox(width: MnemoSpacing.xs),
                IconButton(
                  onPressed: onDismiss,
                  tooltip: dismissTooltip,
                  color: semantic.onDangerContainer,
                  icon: const Icon(Icons.close_rounded),
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }
}

/// An animated loading placeholder that honors reduced-motion preferences.
class MnemoSkeleton extends StatefulWidget {
  const MnemoSkeleton({
    super.key,
    this.width = double.infinity,
    this.height = 16,
    this.borderRadius,
    this.semanticLabel = '正在加载',
  }) : assert(width > 0),
       assert(height > 0);

  const MnemoSkeleton.circle({
    super.key,
    required double diameter,
    this.semanticLabel = '正在加载',
  }) : width = diameter,
       height = diameter,
       borderRadius = const BorderRadius.all(Radius.circular(MnemoRadius.pill)),
       assert(diameter > 0);

  final double width;
  final double height;
  final BorderRadiusGeometry? borderRadius;
  final String semanticLabel;

  @override
  State<MnemoSkeleton> createState() => _MnemoSkeletonState();
}

class _MnemoSkeletonState extends State<MnemoSkeleton>
    with SingleTickerProviderStateMixin {
  late final AnimationController _controller = AnimationController(
    vsync: this,
    duration: MnemoDuration.skeleton,
  );
  bool _reduceMotion = false;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final bool reduceMotion =
        MediaQuery.maybeOf(context)?.disableAnimations ?? false;
    if (_reduceMotion == reduceMotion &&
        (_controller.isAnimating || reduceMotion)) {
      return;
    }
    _reduceMotion = reduceMotion;
    if (reduceMotion) {
      _controller
        ..stop()
        ..value = 0.5;
    } else {
      unawaited(_controller.repeat());
    }
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final MnemoSemanticColors semantic = context.mnemoColors;
    final BorderRadiusGeometry radius =
        widget.borderRadius ?? BorderRadius.circular(MnemoRadius.xs);

    return Semantics(
      container: true,
      label: widget.semanticLabel,
      child: ExcludeSemantics(
        child: SizedBox(
          width: widget.width,
          height: widget.height,
          child: ClipRRect(
            borderRadius: radius,
            child: _reduceMotion
                ? ColoredBox(color: semantic.skeletonBase)
                : AnimatedBuilder(
                    animation: _controller,
                    builder: (BuildContext context, Widget? child) {
                      final double position = (_controller.value * 3) - 1.5;
                      return DecoratedBox(
                        decoration: BoxDecoration(
                          gradient: LinearGradient(
                            begin: Alignment(position - 1, 0),
                            end: Alignment(position + 1, 0),
                            colors: <Color>[
                              semantic.skeletonBase,
                              semantic.skeletonHighlight,
                              semantic.skeletonBase,
                            ],
                            stops: const <double>[0.16, 0.5, 0.84],
                          ),
                        ),
                      );
                    },
                  ),
          ),
        ),
      ),
    );
  }
}

/// A ready-to-use skeleton for card and list loading states.
class MnemoSkeletonList extends StatelessWidget {
  const MnemoSkeletonList({
    super.key,
    this.itemCount = 3,
    this.showLeading = true,
  }) : assert(itemCount > 0);

  final int itemCount;
  final bool showLeading;

  @override
  Widget build(BuildContext context) {
    return Semantics(
      container: true,
      label: '正在加载列表',
      child: ExcludeSemantics(
        child: Column(
          children: List<Widget>.generate(itemCount, (int index) {
            return Padding(
              padding: EdgeInsets.only(
                bottom: index == itemCount - 1 ? 0 : MnemoSpacing.md,
              ),
              child: Row(
                children: <Widget>[
                  if (showLeading) ...<Widget>[
                    const MnemoSkeleton.circle(diameter: 44),
                    const SizedBox(width: MnemoSpacing.sm),
                  ],
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: const <Widget>[
                        FractionallySizedBox(
                          widthFactor: 0.72,
                          child: MnemoSkeleton(height: 14),
                        ),
                        SizedBox(height: MnemoSpacing.xs),
                        FractionallySizedBox(
                          widthFactor: 0.46,
                          child: MnemoSkeleton(height: 11),
                        ),
                      ],
                    ),
                  ),
                ],
              ),
            );
          }),
        ),
      ),
    );
  }
}
