import 'package:flutter/material.dart';

/// The static MnemoNAS brand palette.
///
/// Feature code should normally use [ColorScheme] or [MnemoSemanticColors]
/// instead of referencing these values directly.
abstract final class MnemoPalette {
  static const Color brand = Color(0xFF4F6FDB);
  static const Color brandLight = Color(0xFF7AA2E3);
  static const Color brandDark = Color(0xFF3553B5);
  static const Color aurora = Color(0xFF14B8A6);
  static const Color starlight = Color(0xFFD6A642);
  static const Color rose = Color(0xFFD45A7F);

  static const Color lightCanvas = Color(0xFFF2F5FA);
  static const Color lightSurface = Color(0xFFFFFFFF);
  static const Color lightSurfaceMuted = Color(0xFFF7F8FB);
  static const Color lightSurfaceRaised = Color(0xFFFCFDFF);
  static const Color lightText = Color(0xFF111827);
  static const Color lightTextMuted = Color(0xFF5D6675);
  static const Color lightOutline = Color(0xFFDCE2EC);

  static const Color darkCanvas = Color(0xFF0B1120);
  static const Color darkSurface = Color(0xFF111827);
  static const Color darkSurfaceMuted = Color(0xFF151F32);
  static const Color darkSurfaceRaised = Color(0xFF19243A);
  static const Color darkText = Color(0xFFF4F6FA);
  static const Color darkTextMuted = Color(0xFFA9B4C5);
  static const Color darkOutline = Color(0xFF344158);

  static const Color success = Color(0xFF0E9F6E);
  static const Color warning = Color(0xFFD58A08);
  static const Color danger = Color(0xFFE13D5B);
  static const Color info = Color(0xFF3D76D8);
}

/// Spacing values based on a four-pixel grid.
abstract final class MnemoSpacing {
  static const double xxs = 4;
  static const double xs = 8;
  static const double sm = 12;
  static const double md = 16;
  static const double lg = 20;
  static const double xl = 24;
  static const double xxl = 32;
  static const double xxxl = 40;
  static const double display = 56;
}

/// Corner radius values shared by surfaces and controls.
abstract final class MnemoRadius {
  static const double xs = 8;
  static const double sm = 12;
  static const double md = 16;
  static const double lg = 22;
  static const double xl = 28;
  static const double pill = 999;
}

/// Material elevation levels used when physical elevation is required.
abstract final class MnemoElevation {
  static const double flat = 0;
  static const double low = 1;
  static const double medium = 4;
  static const double high = 8;
  static const double modal = 12;
}

/// Minimum dimensions for accessible interactive controls.
abstract final class MnemoControlSize {
  static const double minimumTouchTarget = 48;
  static const double primaryButtonHeight = 52;
}

/// Layout breakpoints shared by Android and desktop clients.
abstract final class MnemoBreakpoint {
  static const double compact = 600;
  static const double expanded = 1024;
  static const double contentMax = 1280;
  static const double readingMax = 720;
}

/// Standard animation durations.
abstract final class MnemoDuration {
  static const Duration quick = Duration(milliseconds: 120);
  static const Duration standard = Duration(milliseconds: 220);
  static const Duration deliberate = Duration(milliseconds: 360);
  static const Duration skeleton = Duration(milliseconds: 1350);
}

/// System-font fallbacks that provide consistent Latin and CJK coverage.
abstract final class MnemoTypography {
  static const List<String> fontFallback = <String>[
    'Inter',
    'SF Pro Display',
    'Segoe UI',
    'Noto Sans CJK SC',
    'Noto Sans SC',
    'Roboto',
    'sans-serif',
  ];

  static TextTheme textTheme(Color color) {
    TextStyle style({
      required double size,
      required FontWeight weight,
      required double height,
      double? letterSpacing,
    }) {
      return TextStyle(
        color: color,
        fontFamilyFallback: fontFallback,
        fontSize: size,
        fontWeight: weight,
        height: height,
        letterSpacing: letterSpacing,
      );
    }

    return TextTheme(
      displayLarge: style(
        size: 52,
        weight: FontWeight.w700,
        height: 1.08,
        letterSpacing: -1.2,
      ),
      displayMedium: style(
        size: 44,
        weight: FontWeight.w700,
        height: 1.1,
        letterSpacing: -0.8,
      ),
      displaySmall: style(
        size: 36,
        weight: FontWeight.w700,
        height: 1.15,
        letterSpacing: -0.5,
      ),
      headlineLarge: style(
        size: 30,
        weight: FontWeight.w700,
        height: 1.2,
        letterSpacing: -0.35,
      ),
      headlineMedium: style(
        size: 26,
        weight: FontWeight.w700,
        height: 1.22,
        letterSpacing: -0.25,
      ),
      headlineSmall: style(
        size: 22,
        weight: FontWeight.w700,
        height: 1.28,
        letterSpacing: -0.15,
      ),
      titleLarge: style(size: 20, weight: FontWeight.w700, height: 1.3),
      titleMedium: style(size: 16, weight: FontWeight.w600, height: 1.35),
      titleSmall: style(size: 14, weight: FontWeight.w600, height: 1.4),
      bodyLarge: style(size: 16, weight: FontWeight.w400, height: 1.55),
      bodyMedium: style(size: 14, weight: FontWeight.w400, height: 1.5),
      bodySmall: style(size: 12, weight: FontWeight.w400, height: 1.45),
      labelLarge: style(size: 15, weight: FontWeight.w600, height: 1.25),
      labelMedium: style(size: 13, weight: FontWeight.w600, height: 1.25),
      labelSmall: style(
        size: 11,
        weight: FontWeight.w600,
        height: 1.3,
        letterSpacing: 0.15,
      ),
    );
  }
}

/// Additional semantic colors that are not represented by Material's
/// [ColorScheme].
@immutable
class MnemoSemanticColors extends ThemeExtension<MnemoSemanticColors> {
  const MnemoSemanticColors({
    required this.success,
    required this.onSuccess,
    required this.successContainer,
    required this.onSuccessContainer,
    required this.warning,
    required this.onWarning,
    required this.warningContainer,
    required this.onWarningContainer,
    required this.info,
    required this.onInfo,
    required this.infoContainer,
    required this.onInfoContainer,
    required this.dangerContainer,
    required this.onDangerContainer,
    required this.surfaceMuted,
    required this.surfaceRaised,
    required this.skeletonBase,
    required this.skeletonHighlight,
    required this.brandGradient,
    required this.softShadow,
    required this.raisedShadow,
  });

  factory MnemoSemanticColors.light() {
    return const MnemoSemanticColors(
      success: MnemoPalette.success,
      onSuccess: Colors.white,
      successContainer: Color(0xFFDDF8ED),
      onSuccessContainer: Color(0xFF07543C),
      warning: MnemoPalette.warning,
      onWarning: Color(0xFF231A00),
      warningContainer: Color(0xFFFFF0C7),
      onWarningContainer: Color(0xFF664500),
      info: MnemoPalette.info,
      onInfo: Colors.white,
      infoContainer: Color(0xFFE3EDFF),
      onInfoContainer: Color(0xFF174582),
      dangerContainer: Color(0xFFFFE5EA),
      onDangerContainer: Color(0xFF8D1933),
      surfaceMuted: MnemoPalette.lightSurfaceMuted,
      surfaceRaised: MnemoPalette.lightSurfaceRaised,
      skeletonBase: Color(0xFFE7EBF2),
      skeletonHighlight: Color(0xFFF7F9FC),
      brandGradient: LinearGradient(
        begin: Alignment.topLeft,
        end: Alignment.bottomRight,
        colors: <Color>[MnemoPalette.brand, MnemoPalette.aurora],
      ),
      softShadow: <BoxShadow>[
        BoxShadow(
          color: Color(0x100F172A),
          blurRadius: 18,
          offset: Offset(0, 6),
        ),
      ],
      raisedShadow: <BoxShadow>[
        BoxShadow(
          color: Color(0x1A0F172A),
          blurRadius: 28,
          offset: Offset(0, 12),
        ),
      ],
    );
  }

  factory MnemoSemanticColors.dark() {
    return const MnemoSemanticColors(
      success: Color(0xFF43D5A1),
      onSuccess: Color(0xFF042C20),
      successContainer: Color(0xFF143E32),
      onSuccessContainer: Color(0xFF9BE8CE),
      warning: Color(0xFFF2BE56),
      onWarning: Color(0xFF342400),
      warningContainer: Color(0xFF4B3912),
      onWarningContainer: Color(0xFFFFDEA0),
      info: Color(0xFF8DB7FF),
      onInfo: Color(0xFF102D58),
      infoContainer: Color(0xFF203A62),
      onInfoContainer: Color(0xFFC7DAFF),
      dangerContainer: Color(0xFF542433),
      onDangerContainer: Color(0xFFFFB4C1),
      surfaceMuted: MnemoPalette.darkSurfaceMuted,
      surfaceRaised: MnemoPalette.darkSurfaceRaised,
      skeletonBase: Color(0xFF222E43),
      skeletonHighlight: Color(0xFF344158),
      brandGradient: LinearGradient(
        begin: Alignment.topLeft,
        end: Alignment.bottomRight,
        colors: <Color>[Color(0xFF718BEB), Color(0xFF22C7B5)],
      ),
      softShadow: <BoxShadow>[
        BoxShadow(
          color: Color(0x52050A14),
          blurRadius: 24,
          offset: Offset(0, 10),
        ),
      ],
      raisedShadow: <BoxShadow>[
        BoxShadow(
          color: Color(0x73050A14),
          blurRadius: 34,
          offset: Offset(0, 16),
        ),
      ],
    );
  }

  final Color success;
  final Color onSuccess;
  final Color successContainer;
  final Color onSuccessContainer;
  final Color warning;
  final Color onWarning;
  final Color warningContainer;
  final Color onWarningContainer;
  final Color info;
  final Color onInfo;
  final Color infoContainer;
  final Color onInfoContainer;
  final Color dangerContainer;
  final Color onDangerContainer;
  final Color surfaceMuted;
  final Color surfaceRaised;
  final Color skeletonBase;
  final Color skeletonHighlight;
  final Gradient brandGradient;
  final List<BoxShadow> softShadow;
  final List<BoxShadow> raisedShadow;

  @override
  MnemoSemanticColors copyWith({
    Color? success,
    Color? onSuccess,
    Color? successContainer,
    Color? onSuccessContainer,
    Color? warning,
    Color? onWarning,
    Color? warningContainer,
    Color? onWarningContainer,
    Color? info,
    Color? onInfo,
    Color? infoContainer,
    Color? onInfoContainer,
    Color? dangerContainer,
    Color? onDangerContainer,
    Color? surfaceMuted,
    Color? surfaceRaised,
    Color? skeletonBase,
    Color? skeletonHighlight,
    Gradient? brandGradient,
    List<BoxShadow>? softShadow,
    List<BoxShadow>? raisedShadow,
  }) {
    return MnemoSemanticColors(
      success: success ?? this.success,
      onSuccess: onSuccess ?? this.onSuccess,
      successContainer: successContainer ?? this.successContainer,
      onSuccessContainer: onSuccessContainer ?? this.onSuccessContainer,
      warning: warning ?? this.warning,
      onWarning: onWarning ?? this.onWarning,
      warningContainer: warningContainer ?? this.warningContainer,
      onWarningContainer: onWarningContainer ?? this.onWarningContainer,
      info: info ?? this.info,
      onInfo: onInfo ?? this.onInfo,
      infoContainer: infoContainer ?? this.infoContainer,
      onInfoContainer: onInfoContainer ?? this.onInfoContainer,
      dangerContainer: dangerContainer ?? this.dangerContainer,
      onDangerContainer: onDangerContainer ?? this.onDangerContainer,
      surfaceMuted: surfaceMuted ?? this.surfaceMuted,
      surfaceRaised: surfaceRaised ?? this.surfaceRaised,
      skeletonBase: skeletonBase ?? this.skeletonBase,
      skeletonHighlight: skeletonHighlight ?? this.skeletonHighlight,
      brandGradient: brandGradient ?? this.brandGradient,
      softShadow: softShadow ?? this.softShadow,
      raisedShadow: raisedShadow ?? this.raisedShadow,
    );
  }

  @override
  MnemoSemanticColors lerp(covariant MnemoSemanticColors? other, double t) {
    if (other == null) {
      return this;
    }
    return MnemoSemanticColors(
      success: Color.lerp(success, other.success, t)!,
      onSuccess: Color.lerp(onSuccess, other.onSuccess, t)!,
      successContainer: Color.lerp(
        successContainer,
        other.successContainer,
        t,
      )!,
      onSuccessContainer: Color.lerp(
        onSuccessContainer,
        other.onSuccessContainer,
        t,
      )!,
      warning: Color.lerp(warning, other.warning, t)!,
      onWarning: Color.lerp(onWarning, other.onWarning, t)!,
      warningContainer: Color.lerp(
        warningContainer,
        other.warningContainer,
        t,
      )!,
      onWarningContainer: Color.lerp(
        onWarningContainer,
        other.onWarningContainer,
        t,
      )!,
      info: Color.lerp(info, other.info, t)!,
      onInfo: Color.lerp(onInfo, other.onInfo, t)!,
      infoContainer: Color.lerp(infoContainer, other.infoContainer, t)!,
      onInfoContainer: Color.lerp(onInfoContainer, other.onInfoContainer, t)!,
      dangerContainer: Color.lerp(dangerContainer, other.dangerContainer, t)!,
      onDangerContainer: Color.lerp(
        onDangerContainer,
        other.onDangerContainer,
        t,
      )!,
      surfaceMuted: Color.lerp(surfaceMuted, other.surfaceMuted, t)!,
      surfaceRaised: Color.lerp(surfaceRaised, other.surfaceRaised, t)!,
      skeletonBase: Color.lerp(skeletonBase, other.skeletonBase, t)!,
      skeletonHighlight: Color.lerp(
        skeletonHighlight,
        other.skeletonHighlight,
        t,
      )!,
      brandGradient: Gradient.lerp(brandGradient, other.brandGradient, t)!,
      softShadow: BoxShadow.lerpList(softShadow, other.softShadow, t)!,
      raisedShadow: BoxShadow.lerpList(raisedShadow, other.raisedShadow, t)!,
    );
  }
}

/// Accesses MnemoNAS-specific theme values with a safe brightness fallback.
extension MnemoThemeContext on BuildContext {
  MnemoSemanticColors get mnemoColors {
    final ThemeData theme = Theme.of(this);
    return theme.extension<MnemoSemanticColors>() ??
        (theme.brightness == Brightness.dark
            ? MnemoSemanticColors.dark()
            : MnemoSemanticColors.light());
  }
}
