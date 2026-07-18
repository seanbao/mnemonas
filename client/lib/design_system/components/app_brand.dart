import 'package:flutter/material.dart';

import '../tokens/mnemo_tokens.dart';

enum AppBrandSize { compact, regular, hero }

enum AppBrandLayout { horizontal, stacked, markOnly }

/// The MnemoNAS wordmark and code-rendered storage mark.
class AppBrand extends StatelessWidget {
  const AppBrand({
    super.key,
    this.size = AppBrandSize.regular,
    this.layout = AppBrandLayout.horizontal,
    this.subtitle = '私有存储',
    this.showSubtitle = true,
    this.foregroundColor,
  });

  final AppBrandSize size;
  final AppBrandLayout layout;
  final String subtitle;
  final bool showSubtitle;
  final Color? foregroundColor;

  @override
  Widget build(BuildContext context) {
    final _BrandMetrics metrics = _BrandMetrics.forSize(size);
    final Color foreground =
        foregroundColor ?? Theme.of(context).colorScheme.onSurface;
    final Widget mark = MnemoMark(
      size: metrics.markSize,
      excludeFromSemantics: true,
    );

    Widget content;
    switch (layout) {
      case AppBrandLayout.markOnly:
        content = mark;
      case AppBrandLayout.stacked:
        content = Column(
          mainAxisSize: MainAxisSize.min,
          children: <Widget>[
            mark,
            SizedBox(height: metrics.gap),
            _BrandText(
              metrics: metrics,
              subtitle: subtitle,
              showSubtitle: showSubtitle,
              foreground: foreground,
              alignment: TextAlign.center,
            ),
          ],
        );
      case AppBrandLayout.horizontal:
        content = Row(
          mainAxisSize: MainAxisSize.min,
          children: <Widget>[
            mark,
            SizedBox(width: metrics.gap),
            Flexible(
              child: _BrandText(
                metrics: metrics,
                subtitle: subtitle,
                showSubtitle: showSubtitle,
                foreground: foreground,
                alignment: TextAlign.start,
              ),
            ),
          ],
        );
    }

    final String semanticLabel =
        showSubtitle && layout != AppBrandLayout.markOnly
        ? 'MnemoNAS，$subtitle'
        : 'MnemoNAS';
    return Semantics(
      image: true,
      label: semanticLabel,
      child: ExcludeSemantics(child: content),
    );
  }
}

class _BrandText extends StatelessWidget {
  const _BrandText({
    required this.metrics,
    required this.subtitle,
    required this.showSubtitle,
    required this.foreground,
    required this.alignment,
  });

  final _BrandMetrics metrics;
  final String subtitle;
  final bool showSubtitle;
  final Color foreground;
  final TextAlign alignment;

  @override
  Widget build(BuildContext context) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: alignment == TextAlign.center
          ? CrossAxisAlignment.center
          : CrossAxisAlignment.start,
      children: <Widget>[
        Text(
          'MnemoNAS',
          maxLines: 1,
          overflow: TextOverflow.fade,
          softWrap: false,
          textAlign: alignment,
          style: TextStyle(
            color: foreground,
            fontFamilyFallback: MnemoTypography.fontFallback,
            fontSize: metrics.titleSize,
            fontWeight: FontWeight.w700,
            height: 1.15,
            letterSpacing: -0.25,
          ),
        ),
        if (showSubtitle) ...<Widget>[
          SizedBox(height: metrics.subtitleGap),
          Text(
            subtitle,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            textAlign: alignment,
            style: TextStyle(
              color: foreground.withValues(alpha: 0.68),
              fontFamilyFallback: MnemoTypography.fontFallback,
              fontSize: metrics.subtitleSize,
              fontWeight: FontWeight.w500,
              height: 1.2,
              letterSpacing: 0.18,
            ),
          ),
        ],
      ],
    );
  }
}

/// A scalable, asset-free MnemoNAS brand mark.
class MnemoMark extends StatelessWidget {
  const MnemoMark({
    super.key,
    this.size = 44,
    this.excludeFromSemantics = false,
  }) : assert(size > 0);

  final double size;
  final bool excludeFromSemantics;

  @override
  Widget build(BuildContext context) {
    final Widget mark = RepaintBoundary(
      child: SizedBox.square(
        dimension: size,
        child: CustomPaint(
          painter: _MnemoMarkPainter(
            gradient: context.mnemoColors.brandGradient,
            brightness: Theme.of(context).brightness,
          ),
        ),
      ),
    );
    if (excludeFromSemantics) {
      return ExcludeSemantics(child: mark);
    }
    return Semantics(image: true, label: 'MnemoNAS', child: mark);
  }
}

class _MnemoMarkPainter extends CustomPainter {
  const _MnemoMarkPainter({required this.gradient, required this.brightness});

  final Gradient gradient;
  final Brightness brightness;

  @override
  void paint(Canvas canvas, Size size) {
    final Rect bounds = Offset.zero & size;
    final double radius = size.shortestSide * 0.27;
    final RRect background = RRect.fromRectAndRadius(
      bounds,
      Radius.circular(radius),
    );

    canvas.drawRRect(
      background,
      Paint()..shader = gradient.createShader(bounds),
    );
    canvas.drawRRect(
      background.deflate(size.shortestSide * 0.025),
      Paint()
        ..color = Colors.white.withValues(
          alpha: brightness == Brightness.dark ? 0.19 : 0.25,
        )
        ..style = PaintingStyle.stroke
        ..strokeWidth = size.shortestSide * 0.025,
    );

    final Paint linePaint = Paint()
      ..color = Colors.white
      ..style = PaintingStyle.stroke
      ..strokeCap = StrokeCap.round
      ..strokeJoin = StrokeJoin.round
      ..strokeWidth = size.shortestSide * 0.075;
    final double left = size.width * 0.245;
    final double right = size.width * 0.755;
    final double top = size.height * 0.29;
    final double middle = size.height * 0.5;
    final double bottom = size.height * 0.71;

    canvas.drawLine(Offset(left, top), Offset(right, top), linePaint);
    canvas.drawLine(Offset(left, middle), Offset(right, middle), linePaint);
    canvas.drawLine(
      Offset(left, bottom),
      Offset(size.width * 0.57, bottom),
      linePaint,
    );
    canvas.drawCircle(
      Offset(size.width * 0.735, bottom),
      size.shortestSide * 0.052,
      Paint()..color = Colors.white,
    );
  }

  @override
  bool shouldRepaint(covariant _MnemoMarkPainter oldDelegate) {
    return oldDelegate.gradient != gradient ||
        oldDelegate.brightness != brightness;
  }
}

class _BrandMetrics {
  const _BrandMetrics({
    required this.markSize,
    required this.titleSize,
    required this.subtitleSize,
    required this.gap,
    required this.subtitleGap,
  });

  factory _BrandMetrics.forSize(AppBrandSize size) {
    return switch (size) {
      AppBrandSize.compact => const _BrandMetrics(
        markSize: 36,
        titleSize: 16,
        subtitleSize: 10,
        gap: 10,
        subtitleGap: 1,
      ),
      AppBrandSize.regular => const _BrandMetrics(
        markSize: 44,
        titleSize: 19,
        subtitleSize: 11,
        gap: 12,
        subtitleGap: 2,
      ),
      AppBrandSize.hero => const _BrandMetrics(
        markSize: 72,
        titleSize: 30,
        subtitleSize: 14,
        gap: 18,
        subtitleGap: 4,
      ),
    };
  }

  final double markSize;
  final double titleSize;
  final double subtitleSize;
  final double gap;
  final double subtitleGap;
}
