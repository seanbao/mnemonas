import 'package:flutter/material.dart';

import '../tokens/mnemo_tokens.dart';

enum MnemoWindowClass { compact, medium, expanded }

typedef MnemoAdaptiveWidgetBuilder =
    Widget Function(BuildContext context, MnemoWindowClass windowClass);

/// Shared responsive rules for phone, tablet, and desktop layouts.
abstract final class MnemoAdaptive {
  static MnemoWindowClass windowClassFor(double width) {
    if (width < MnemoBreakpoint.compact) {
      return MnemoWindowClass.compact;
    }
    if (width < MnemoBreakpoint.expanded) {
      return MnemoWindowClass.medium;
    }
    return MnemoWindowClass.expanded;
  }

  static EdgeInsets pagePaddingFor(MnemoWindowClass windowClass) {
    return switch (windowClass) {
      MnemoWindowClass.compact => const EdgeInsets.all(MnemoSpacing.md),
      MnemoWindowClass.medium => const EdgeInsets.all(MnemoSpacing.xl),
      MnemoWindowClass.expanded => const EdgeInsets.symmetric(
        horizontal: MnemoSpacing.xxl,
        vertical: MnemoSpacing.xl,
      ),
    };
  }

  static int gridColumnsFor(MnemoWindowClass windowClass) {
    return switch (windowClass) {
      MnemoWindowClass.compact => 1,
      MnemoWindowClass.medium => 2,
      MnemoWindowClass.expanded => 3,
    };
  }
}

/// Resolves the active [MnemoWindowClass] from the available layout width.
class MnemoAdaptiveBuilder extends StatelessWidget {
  const MnemoAdaptiveBuilder({super.key, required this.builder});

  final MnemoAdaptiveWidgetBuilder builder;

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (BuildContext context, BoxConstraints constraints) {
        final double width = constraints.maxWidth.isFinite
            ? constraints.maxWidth
            : MediaQuery.sizeOf(context).width;
        return builder(context, MnemoAdaptive.windowClassFor(width));
      },
    );
  }
}

/// Centers content on wide windows while retaining phone-safe page padding.
class MnemoContentFrame extends StatelessWidget {
  const MnemoContentFrame({
    super.key,
    required this.child,
    this.maxWidth = MnemoBreakpoint.contentMax,
    this.useSafeArea = true,
    this.padding,
    this.alignment = Alignment.topCenter,
  });

  final Widget child;
  final double maxWidth;
  final bool useSafeArea;
  final EdgeInsetsGeometry? padding;
  final AlignmentGeometry alignment;

  @override
  Widget build(BuildContext context) {
    Widget result = MnemoAdaptiveBuilder(
      builder: (BuildContext context, MnemoWindowClass windowClass) {
        return Align(
          alignment: alignment,
          child: ConstrainedBox(
            constraints: BoxConstraints(maxWidth: maxWidth),
            child: Padding(
              padding: padding ?? MnemoAdaptive.pagePaddingFor(windowClass),
              child: child,
            ),
          ),
        );
      },
    );
    if (useSafeArea) {
      result = SafeArea(child: result);
    }
    return result;
  }
}
