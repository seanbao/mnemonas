import 'package:flutter/material.dart';

import '../../design_system/design_system.dart';

enum AppDestination {
  home('首页', Icons.home_outlined, Icons.home_rounded),
  files('文件', Icons.folder_outlined, Icons.folder_rounded),
  trash('回收站', Icons.delete_outline_rounded, Icons.delete_rounded),
  account('我的', Icons.person_outline_rounded, Icons.person_rounded);

  const AppDestination(this.label, this.icon, this.selectedIcon);

  final String label;
  final IconData icon;
  final IconData selectedIcon;
}

class AppShell extends StatelessWidget {
  const AppShell({
    super.key,
    required this.destination,
    required this.onDestinationSelected,
    required this.child,
    this.onSearch,
    this.title,
    this.actions = const <Widget>[],
    this.floatingActionButton,
  });

  final AppDestination destination;
  final ValueChanged<AppDestination> onDestinationSelected;
  final VoidCallback? onSearch;
  final Widget child;
  final String? title;
  final List<Widget> actions;
  final Widget? floatingActionButton;

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (BuildContext context, BoxConstraints constraints) {
        final MnemoWindowClass windowClass = MnemoAdaptive.windowClassFor(
          constraints.maxWidth,
        );
        final bool compact = windowClass == MnemoWindowClass.compact;
        final bool extended = windowClass == MnemoWindowClass.expanded;
        return Scaffold(
          appBar: AppBar(
            automaticallyImplyLeading: false,
            title: compact
                ? Text(title ?? destination.label)
                : Row(
                    children: <Widget>[
                      const AppBrand(
                        size: AppBrandSize.compact,
                        showSubtitle: false,
                      ),
                      const SizedBox(width: MnemoSpacing.xl),
                      Text(title ?? destination.label),
                    ],
                  ),
            actions: <Widget>[
              if (onSearch != null)
                IconButton(
                  key: const Key('app-shell-search'),
                  onPressed: onSearch,
                  tooltip: '搜索文件',
                  icon: const Icon(Icons.search_rounded),
                ),
              ...actions,
              const SizedBox(width: MnemoSpacing.xs),
            ],
          ),
          body: compact
              ? child
              : Row(
                  children: <Widget>[
                    NavigationRail(
                      extended: extended,
                      selectedIndex: destination.index,
                      onDestinationSelected: (int index) =>
                          onDestinationSelected(AppDestination.values[index]),
                      labelType: extended
                          ? NavigationRailLabelType.none
                          : NavigationRailLabelType.all,
                      destinations: <NavigationRailDestination>[
                        for (final item in AppDestination.values)
                          NavigationRailDestination(
                            icon: Icon(item.icon),
                            selectedIcon: Icon(item.selectedIcon),
                            label: Text(item.label),
                          ),
                      ],
                    ),
                    VerticalDivider(
                      width: 1,
                      color: Theme.of(context).colorScheme.outlineVariant,
                    ),
                    Expanded(child: child),
                  ],
                ),
          bottomNavigationBar: compact
              ? NavigationBar(
                  selectedIndex: destination.index,
                  onDestinationSelected: (int index) =>
                      onDestinationSelected(AppDestination.values[index]),
                  destinations: <NavigationDestination>[
                    for (final item in AppDestination.values)
                      NavigationDestination(
                        icon: Icon(item.icon),
                        selectedIcon: Icon(item.selectedIcon),
                        label: item.label,
                      ),
                  ],
                )
              : null,
          floatingActionButton: floatingActionButton,
        );
      },
    );
  }
}
