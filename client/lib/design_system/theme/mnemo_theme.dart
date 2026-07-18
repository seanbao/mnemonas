import 'package:flutter/material.dart';

import '../tokens/mnemo_tokens.dart';

/// Material 3 themes for the MnemoNAS client.
abstract final class MnemoTheme {
  static ThemeData get light => _build(Brightness.light);

  static ThemeData get dark => _build(Brightness.dark);

  static ThemeData _build(Brightness brightness) {
    final bool isDark = brightness == Brightness.dark;
    final MnemoSemanticColors semantic = isDark
        ? MnemoSemanticColors.dark()
        : MnemoSemanticColors.light();
    final ColorScheme colors = _colorScheme(brightness);
    final TextTheme textTheme = MnemoTypography.textTheme(colors.onSurface);
    final BorderRadius controlRadius = BorderRadius.circular(MnemoRadius.sm);
    final BorderRadius surfaceRadius = BorderRadius.circular(MnemoRadius.md);

    return ThemeData(
      useMaterial3: true,
      brightness: brightness,
      colorScheme: colors,
      scaffoldBackgroundColor: isDark
          ? MnemoPalette.darkCanvas
          : MnemoPalette.lightCanvas,
      canvasColor: colors.surface,
      cardColor: colors.surface,
      dividerColor: colors.outlineVariant,
      disabledColor: colors.onSurface.withValues(alpha: 0.38),
      focusColor: colors.primary.withValues(alpha: 0.16),
      hoverColor: colors.primary.withValues(alpha: 0.08),
      highlightColor: colors.primary.withValues(alpha: 0.08),
      splashColor: colors.primary.withValues(alpha: 0.12),
      visualDensity: VisualDensity.adaptivePlatformDensity,
      materialTapTargetSize: MaterialTapTargetSize.padded,
      textTheme: textTheme,
      primaryTextTheme: MnemoTypography.textTheme(colors.onPrimary),
      extensions: <ThemeExtension<dynamic>>[semantic],
      appBarTheme: AppBarTheme(
        backgroundColor: colors.surface.withValues(alpha: 0.94),
        foregroundColor: colors.onSurface,
        elevation: MnemoElevation.flat,
        scrolledUnderElevation: MnemoElevation.flat,
        surfaceTintColor: Colors.transparent,
        centerTitle: false,
        titleSpacing: MnemoSpacing.md,
        toolbarHeight: 64,
        titleTextStyle: textTheme.titleLarge,
      ),
      cardTheme: CardThemeData(
        color: colors.surface,
        surfaceTintColor: Colors.transparent,
        elevation: MnemoElevation.flat,
        margin: EdgeInsets.zero,
        shape: RoundedRectangleBorder(
          borderRadius: surfaceRadius,
          side: BorderSide(color: colors.outlineVariant),
        ),
      ),
      dividerTheme: DividerThemeData(
        color: colors.outlineVariant,
        space: 1,
        thickness: 1,
      ),
      inputDecorationTheme: InputDecorationTheme(
        filled: true,
        fillColor: semantic.surfaceMuted,
        contentPadding: const EdgeInsets.symmetric(
          horizontal: MnemoSpacing.md,
          vertical: MnemoSpacing.md,
        ),
        labelStyle: textTheme.bodyMedium?.copyWith(
          color: colors.onSurfaceVariant,
        ),
        hintStyle: textTheme.bodyMedium?.copyWith(
          color: colors.onSurfaceVariant.withValues(alpha: 0.8),
        ),
        helperStyle: textTheme.bodySmall?.copyWith(
          color: colors.onSurfaceVariant,
        ),
        errorStyle: textTheme.bodySmall?.copyWith(color: colors.error),
        border: OutlineInputBorder(
          borderRadius: controlRadius,
          borderSide: BorderSide(color: colors.outlineVariant),
        ),
        enabledBorder: OutlineInputBorder(
          borderRadius: controlRadius,
          borderSide: BorderSide(color: colors.outlineVariant),
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: controlRadius,
          borderSide: BorderSide(color: colors.primary, width: 1.6),
        ),
        errorBorder: OutlineInputBorder(
          borderRadius: controlRadius,
          borderSide: BorderSide(color: colors.error),
        ),
        focusedErrorBorder: OutlineInputBorder(
          borderRadius: controlRadius,
          borderSide: BorderSide(color: colors.error, width: 1.6),
        ),
      ),
      filledButtonTheme: FilledButtonThemeData(
        style: ButtonStyle(
          minimumSize: const WidgetStatePropertyAll<Size>(
            Size(MnemoControlSize.minimumTouchTarget, 50),
          ),
          padding: const WidgetStatePropertyAll<EdgeInsetsGeometry>(
            EdgeInsets.symmetric(
              horizontal: MnemoSpacing.lg,
              vertical: MnemoSpacing.sm,
            ),
          ),
          textStyle: WidgetStatePropertyAll<TextStyle?>(textTheme.labelLarge),
          elevation: const WidgetStatePropertyAll<double>(0),
          shape: WidgetStatePropertyAll<OutlinedBorder>(
            RoundedRectangleBorder(borderRadius: controlRadius),
          ),
        ),
      ),
      outlinedButtonTheme: OutlinedButtonThemeData(
        style: ButtonStyle(
          minimumSize: const WidgetStatePropertyAll<Size>(
            Size(MnemoControlSize.minimumTouchTarget, 50),
          ),
          padding: const WidgetStatePropertyAll<EdgeInsetsGeometry>(
            EdgeInsets.symmetric(
              horizontal: MnemoSpacing.lg,
              vertical: MnemoSpacing.sm,
            ),
          ),
          textStyle: WidgetStatePropertyAll<TextStyle?>(textTheme.labelLarge),
          foregroundColor: WidgetStatePropertyAll<Color>(colors.onSurface),
          side: WidgetStatePropertyAll<BorderSide>(
            BorderSide(color: colors.outline),
          ),
          shape: WidgetStatePropertyAll<OutlinedBorder>(
            RoundedRectangleBorder(borderRadius: controlRadius),
          ),
        ),
      ),
      textButtonTheme: TextButtonThemeData(
        style: ButtonStyle(
          minimumSize: const WidgetStatePropertyAll<Size>(
            Size.square(MnemoControlSize.minimumTouchTarget),
          ),
          padding: const WidgetStatePropertyAll<EdgeInsetsGeometry>(
            EdgeInsets.symmetric(horizontal: MnemoSpacing.md),
          ),
          textStyle: WidgetStatePropertyAll<TextStyle?>(textTheme.labelLarge),
          shape: WidgetStatePropertyAll<OutlinedBorder>(
            RoundedRectangleBorder(borderRadius: controlRadius),
          ),
        ),
      ),
      iconButtonTheme: IconButtonThemeData(
        style: ButtonStyle(
          minimumSize: const WidgetStatePropertyAll<Size>(
            Size.square(MnemoControlSize.minimumTouchTarget),
          ),
          shape: WidgetStatePropertyAll<OutlinedBorder>(
            RoundedRectangleBorder(borderRadius: controlRadius),
          ),
        ),
      ),
      floatingActionButtonTheme: FloatingActionButtonThemeData(
        backgroundColor: colors.primary,
        foregroundColor: colors.onPrimary,
        elevation: MnemoElevation.medium,
        focusElevation: 6,
        hoverElevation: 6,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(MnemoRadius.md),
        ),
      ),
      chipTheme: ChipThemeData(
        backgroundColor: semantic.surfaceMuted,
        selectedColor: colors.primaryContainer,
        disabledColor: colors.onSurface.withValues(alpha: 0.08),
        side: BorderSide(color: colors.outlineVariant),
        shape: const StadiumBorder(),
        padding: const EdgeInsets.symmetric(
          horizontal: MnemoSpacing.sm,
          vertical: MnemoSpacing.xxs,
        ),
        labelStyle: textTheme.labelMedium,
        secondaryLabelStyle: textTheme.labelMedium?.copyWith(
          color: colors.onPrimaryContainer,
        ),
        checkmarkColor: colors.onPrimaryContainer,
        showCheckmark: true,
      ),
      listTileTheme: ListTileThemeData(
        contentPadding: const EdgeInsets.symmetric(
          horizontal: MnemoSpacing.md,
          vertical: MnemoSpacing.xxs,
        ),
        minTileHeight: 56,
        iconColor: colors.onSurfaceVariant,
        textColor: colors.onSurface,
        titleTextStyle: textTheme.bodyLarge?.copyWith(
          fontWeight: FontWeight.w600,
        ),
        subtitleTextStyle: textTheme.bodyMedium?.copyWith(
          color: colors.onSurfaceVariant,
        ),
        shape: RoundedRectangleBorder(borderRadius: controlRadius),
      ),
      navigationBarTheme: NavigationBarThemeData(
        height: 72,
        elevation: MnemoElevation.flat,
        backgroundColor: colors.surface,
        surfaceTintColor: Colors.transparent,
        indicatorColor: colors.primaryContainer,
        indicatorShape: const StadiumBorder(),
        labelBehavior: NavigationDestinationLabelBehavior.alwaysShow,
        iconTheme: WidgetStateProperty.resolveWith<IconThemeData>((
          Set<WidgetState> states,
        ) {
          return IconThemeData(
            color: states.contains(WidgetState.selected)
                ? colors.onPrimaryContainer
                : colors.onSurfaceVariant,
            size: 24,
          );
        }),
        labelTextStyle: WidgetStateProperty.resolveWith<TextStyle?>((
          Set<WidgetState> states,
        ) {
          return textTheme.labelSmall?.copyWith(
            color: states.contains(WidgetState.selected)
                ? colors.onSurface
                : colors.onSurfaceVariant,
            fontWeight: states.contains(WidgetState.selected)
                ? FontWeight.w700
                : FontWeight.w500,
          );
        }),
      ),
      navigationRailTheme: NavigationRailThemeData(
        backgroundColor: colors.surface,
        elevation: MnemoElevation.flat,
        minWidth: 76,
        minExtendedWidth: 244,
        groupAlignment: -0.85,
        indicatorColor: colors.primaryContainer,
        indicatorShape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(MnemoRadius.sm),
        ),
        selectedIconTheme: IconThemeData(color: colors.onPrimaryContainer),
        unselectedIconTheme: IconThemeData(color: colors.onSurfaceVariant),
        selectedLabelTextStyle: textTheme.labelMedium?.copyWith(
          color: colors.onSurface,
          fontWeight: FontWeight.w700,
        ),
        unselectedLabelTextStyle: textTheme.labelMedium?.copyWith(
          color: colors.onSurfaceVariant,
        ),
      ),
      drawerTheme: DrawerThemeData(
        backgroundColor: colors.surface,
        surfaceTintColor: Colors.transparent,
        elevation: MnemoElevation.flat,
        shape: const RoundedRectangleBorder(
          borderRadius: BorderRadius.horizontal(
            right: Radius.circular(MnemoRadius.lg),
          ),
        ),
      ),
      dialogTheme: DialogThemeData(
        backgroundColor: colors.surface,
        surfaceTintColor: Colors.transparent,
        elevation: MnemoElevation.modal,
        shadowColor: colors.shadow.withValues(alpha: 0.28),
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(MnemoRadius.lg),
        ),
        titleTextStyle: textTheme.headlineSmall,
        contentTextStyle: textTheme.bodyMedium?.copyWith(
          color: colors.onSurfaceVariant,
        ),
      ),
      bottomSheetTheme: BottomSheetThemeData(
        backgroundColor: colors.surface,
        modalBackgroundColor: colors.surface,
        surfaceTintColor: Colors.transparent,
        elevation: 10,
        modalElevation: 14,
        shape: const RoundedRectangleBorder(
          borderRadius: BorderRadius.vertical(
            top: Radius.circular(MnemoRadius.xl),
          ),
        ),
        showDragHandle: true,
      ),
      snackBarTheme: SnackBarThemeData(
        behavior: SnackBarBehavior.floating,
        backgroundColor: colors.inverseSurface,
        contentTextStyle: textTheme.bodyMedium?.copyWith(
          color: colors.onInverseSurface,
        ),
        actionTextColor: colors.inversePrimary,
        elevation: MnemoElevation.high,
        insetPadding: const EdgeInsets.all(MnemoSpacing.md),
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(MnemoRadius.sm),
        ),
      ),
      tooltipTheme: TooltipThemeData(
        waitDuration: const Duration(milliseconds: 450),
        showDuration: const Duration(seconds: 3),
        decoration: BoxDecoration(
          color: colors.inverseSurface,
          borderRadius: BorderRadius.circular(MnemoRadius.xs),
        ),
        textStyle: textTheme.bodySmall?.copyWith(
          color: colors.onInverseSurface,
        ),
        padding: const EdgeInsets.symmetric(
          horizontal: MnemoSpacing.sm,
          vertical: MnemoSpacing.xs,
        ),
      ),
      progressIndicatorTheme: ProgressIndicatorThemeData(
        color: colors.primary,
        linearTrackColor: colors.surfaceContainerHighest,
        circularTrackColor: colors.surfaceContainerHighest,
      ),
      switchTheme: SwitchThemeData(
        thumbColor: WidgetStateProperty.resolveWith<Color?>((
          Set<WidgetState> states,
        ) {
          if (states.contains(WidgetState.disabled)) {
            return colors.onSurface.withValues(alpha: 0.38);
          }
          if (states.contains(WidgetState.selected)) {
            return colors.onPrimary;
          }
          return colors.outline;
        }),
        trackColor: WidgetStateProperty.resolveWith<Color?>((
          Set<WidgetState> states,
        ) {
          if (states.contains(WidgetState.disabled)) {
            return colors.onSurface.withValues(alpha: 0.12);
          }
          if (states.contains(WidgetState.selected)) {
            return colors.primary;
          }
          return colors.surfaceContainerHighest;
        }),
        trackOutlineColor: WidgetStatePropertyAll<Color>(colors.outlineVariant),
      ),
      checkboxTheme: CheckboxThemeData(
        fillColor: WidgetStateProperty.resolveWith<Color?>((
          Set<WidgetState> states,
        ) {
          if (states.contains(WidgetState.selected)) {
            return colors.primary;
          }
          return null;
        }),
        side: BorderSide(color: colors.outline, width: 1.5),
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(MnemoRadius.xs / 2),
        ),
      ),
      scrollbarTheme: ScrollbarThemeData(
        thickness: const WidgetStatePropertyAll<double>(6),
        radius: const Radius.circular(MnemoRadius.pill),
        thumbColor: WidgetStateProperty.resolveWith<Color>((
          Set<WidgetState> states,
        ) {
          return colors.onSurfaceVariant.withValues(
            alpha: states.contains(WidgetState.hovered) ? 0.55 : 0.34,
          );
        }),
      ),
    );
  }

  static ColorScheme _colorScheme(Brightness brightness) {
    if (brightness == Brightness.dark) {
      return ColorScheme.fromSeed(
        seedColor: MnemoPalette.brand,
        brightness: Brightness.dark,
      ).copyWith(
        primary: const Color(0xFFAFC2FF),
        onPrimary: const Color(0xFF16337B),
        primaryContainer: const Color(0xFF2E4DA8),
        onPrimaryContainer: const Color(0xFFDDE5FF),
        secondary: const Color(0xFF69D9CC),
        onSecondary: const Color(0xFF003731),
        secondaryContainer: const Color(0xFF005049),
        onSecondaryContainer: const Color(0xFF8EF7EA),
        tertiary: const Color(0xFFE8C36C),
        onTertiary: const Color(0xFF3D2E00),
        error: const Color(0xFFFFB2BF),
        onError: const Color(0xFF680023),
        errorContainer: const Color(0xFF8E173A),
        onErrorContainer: const Color(0xFFFFD9DF),
        surface: MnemoPalette.darkSurface,
        onSurface: MnemoPalette.darkText,
        surfaceDim: MnemoPalette.darkCanvas,
        surfaceBright: const Color(0xFF263349),
        surfaceContainerLowest: const Color(0xFF080E1A),
        surfaceContainerLow: const Color(0xFF0F1726),
        surfaceContainer: MnemoPalette.darkSurfaceMuted,
        surfaceContainerHigh: MnemoPalette.darkSurfaceRaised,
        surfaceContainerHighest: const Color(0xFF223049),
        onSurfaceVariant: MnemoPalette.darkTextMuted,
        outline: const Color(0xFF718096),
        outlineVariant: MnemoPalette.darkOutline,
        shadow: Colors.black,
        scrim: Colors.black,
        inverseSurface: const Color(0xFFE2E8F3),
        onInverseSurface: const Color(0xFF1E2939),
        inversePrimary: MnemoPalette.brandDark,
        surfaceTint: Colors.transparent,
      );
    }

    return ColorScheme.fromSeed(
      seedColor: MnemoPalette.brand,
      brightness: Brightness.light,
    ).copyWith(
      primary: MnemoPalette.brand,
      onPrimary: Colors.white,
      primaryContainer: const Color(0xFFE5EAFF),
      onPrimaryContainer: const Color(0xFF263C87),
      secondary: const Color(0xFF0C8077),
      onSecondary: Colors.white,
      secondaryContainer: const Color(0xFFD3F6F1),
      onSecondaryContainer: const Color(0xFF07534D),
      tertiary: const Color(0xFF8A6712),
      onTertiary: Colors.white,
      error: const Color(0xFFC4294C),
      onError: Colors.white,
      errorContainer: const Color(0xFFFFD9E0),
      onErrorContainer: const Color(0xFF821631),
      surface: MnemoPalette.lightSurface,
      onSurface: MnemoPalette.lightText,
      surfaceDim: const Color(0xFFE7EAF0),
      surfaceBright: Colors.white,
      surfaceContainerLowest: Colors.white,
      surfaceContainerLow: const Color(0xFFFAFBFE),
      surfaceContainer: MnemoPalette.lightSurfaceMuted,
      surfaceContainerHigh: const Color(0xFFF0F3F8),
      surfaceContainerHighest: const Color(0xFFE8ECF3),
      onSurfaceVariant: MnemoPalette.lightTextMuted,
      outline: const Color(0xFF7A8494),
      outlineVariant: MnemoPalette.lightOutline,
      shadow: const Color(0xFF0F172A),
      scrim: const Color(0xFF0F172A),
      inverseSurface: const Color(0xFF253043),
      onInverseSurface: const Color(0xFFF3F5FA),
      inversePrimary: const Color(0xFFAFC2FF),
      surfaceTint: Colors.transparent,
    );
  }
}
