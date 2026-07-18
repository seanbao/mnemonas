import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/core/trash/trash_models.dart';

void main() {
  group('TrashListing', () {
    test('decodes a strict immutable listing and policy snapshot', () {
      final listing = TrashListing.fromJson(_listingJson());

      expect(listing.count, 2);
      expect(listing.totalSize, 42);
      expect(listing.items.first.id, 'item-a');
      expect(listing.items.first.originalPath, '/docs/report.txt');
      expect(listing.items.first.deletedAt, DateTime.utc(2026, 7, 19, 4));
      expect(listing.items.first.expiresAt, DateTime.utc(2026, 8, 18, 4));
      expect(listing.items.first.hadVersions, isTrue);
      expect(listing.policy.retentionDays, 30);
      expect(listing.policy.retentionEnabled, isTrue);
      expect(listing.policy.retentionMaxSize, 1024);
      expect(listing.policy.autoCleanupEnabled, isTrue);
      expect(
        () => listing.items.add(listing.items.first),
        throwsUnsupportedError,
      );

      final remaining = listing.withoutIds(const <String>['item-a']);
      expect(remaining.items.map((item) => item.id), <String>['item-b']);
      expect(remaining.count, 1);
      expect(remaining.totalSize, 0);
      expect(remaining.policy, same(listing.policy));
    });

    test('rejects invalid IDs, paths, names, and timestamps', () {
      final invalidCases = <void Function(Map<String, dynamic>)>[
        (json) => _firstItem(json)['id'] = 'item/a',
        (json) => _firstItem(json)['id'] = List<String>.filled(129, 'a').join(),
        (json) => _secondItem(json)['id'] = 'item-a',
        (json) => _firstItem(json)['originalPath'] = 'docs/report.txt',
        (json) => _firstItem(json)['originalPath'] = '/docs//report.txt',
        (json) => _firstItem(json)['originalPath'] = '/',
        (json) => _firstItem(json)['name'] = 'different.txt',
        (json) => _firstItem(json)['deletedAt'] = '2026-07-19T12:00:00',
        (json) => _firstItem(json)['deletedAt'] = '2026-02-30T12:00:00Z',
        (json) => _firstItem(json)['expiresAt'] = '2026-07-18T12:00:00Z',
        (json) => _firstItem(json)['expiresAt'] = '2026-08-18T12:00:00+24:00',
      ];

      for (final invalidate in invalidCases) {
        final json = _listingJson();
        invalidate(json);
        expect(
          () => TrashListing.fromJson(json),
          throwsFormatException,
          reason: json.toString(),
        );
      }
    });

    test('rejects invalid sizes, counts, totals, and policy fields', () {
      final invalidCases = <void Function(Map<String, dynamic>)>[
        (json) => _firstItem(json)['size'] = -1,
        (json) => _firstItem(json)['size'] = 42.0,
        (json) => json['count'] = 1,
        (json) => json['totalSize'] = 41,
        (json) => json['retentionDays'] = -1,
        (json) => json['retentionEnabled'] = 1,
        (json) => json['retentionMaxSize'] = -1,
        (json) => json['trashAutoCleanupEnabled'] = null,
      ];

      for (final invalidate in invalidCases) {
        final json = _listingJson();
        invalidate(json);
        expect(
          () => TrashListing.fromJson(json),
          throwsFormatException,
          reason: json.toString(),
        );
      }
    });
  });

  group('TrashSelectionSnapshot', () {
    test('freezes canonical unique IDs in request order', () {
      final source = <String>['item-a', 'item_B', 'item-3'];
      final selection = TrashSelectionSnapshot.fromIds(source);
      source
        ..clear()
        ..add('different');

      expect(selection.ids, ['item-a', 'item_B', 'item-3']);
      expect(selection.toJson(), {
        'ids': ['item-a', 'item_B', 'item-3'],
      });
      expect(() => selection.ids.add('item-4'), throwsUnsupportedError);
    });

    test('can freeze IDs from listing items', () {
      final listing = TrashListing.fromJson(_listingJson());

      expect(TrashSelectionSnapshot.fromItems(listing.items).ids, [
        'item-a',
        'item-b',
      ]);
    });

    test('rejects empty, duplicate, malformed, and oversized selections', () {
      expect(
        () => TrashSelectionSnapshot.fromIds(const []),
        throwsFormatException,
      );
      expect(
        () => TrashSelectionSnapshot.fromIds(const ['item-a', 'item-a']),
        throwsFormatException,
      );
      expect(
        () => TrashSelectionSnapshot.fromIds(const ['item/a']),
        throwsFormatException,
      );
      expect(
        () => TrashSelectionSnapshot.fromIds(
          List<String>.generate(
            maxTrashSelectionIds + 1,
            (index) => 'item-$index',
          ),
        ),
        throwsFormatException,
      );
    });
  });

  group('trash mutation results', () {
    test('validates restore and delete confirmations against expected IDs', () {
      final restored = TrashRestoreResult.fromJson({
        'id': 'item-a',
        'restored': true,
        'warning': true,
      }, expectedId: 'item-a');
      final deleted = TrashDeleteResult.fromJson({
        'id': 'item-b',
        'deleted': true,
      }, expectedId: 'item-b');

      expect(restored.id, 'item-a');
      expect(restored.persistenceWarning, isTrue);
      expect(deleted.id, 'item-b');
      expect(deleted.cleanupWarning, isFalse);
    });

    test(
      'rejects mismatched IDs, unconfirmed actions, and invalid warnings',
      () {
        expect(
          () => TrashRestoreResult.fromJson({
            'id': 'item-b',
            'restored': true,
          }, expectedId: 'item-a'),
          throwsFormatException,
        );
        expect(
          () => TrashRestoreResult.fromJson({
            'id': 'item-a',
            'restored': false,
          }, expectedId: 'item-a'),
          throwsFormatException,
        );
        expect(
          () => TrashDeleteResult.fromJson({
            'id': 'item-a',
            'deleted': true,
            'warning': 1,
          }, expectedId: 'item-a'),
          throwsFormatException,
        );
      },
    );
  });

  group('TrashEmptyResult', () {
    final selection = TrashSelectionSnapshot.fromIds(const [
      'item-a',
      'item-b',
      'item-c',
      'item-d',
    ]);

    test('accepts a complete ordered partition with partial results', () {
      final result = TrashEmptyResult.fromJson(
        _emptyResultJson(),
        selection: selection,
      );

      expect(result.deleted, ['item-a', 'item-c']);
      expect(result.remaining, ['item-b']);
      expect(result.skipped, ['item-d']);
      expect(result.partial, isTrue);
      expect(result.cleanupWarning, isFalse);
      expect(() => result.deleted.add('item-e'), throwsUnsupportedError);
    });

    test('accepts an all-deleted non-partial result', () {
      final allDeleted = _emptyResultJson()
        ..['deleted'] = ['item-a', 'item-b', 'item-c', 'item-d']
        ..['remaining'] = <String>[]
        ..['skipped'] = <String>[]
        ..['deleted_count'] = 4
        ..['remaining_count'] = 0
        ..['skipped_count'] = 0
        ..['partial'] = false
        ..['warning'] = true;

      final result = TrashEmptyResult.fromJson(
        allDeleted,
        selection: selection,
      );

      expect(result.partial, isFalse);
      expect(result.cleanupWarning, isTrue);
    });

    test(
      'rejects incomplete, duplicate, foreign, and unordered partitions',
      () {
        final invalidCases = <void Function(Map<String, dynamic>)>[
          (json) {
            json['skipped'] = <String>[];
            json['skipped_count'] = 0;
          },
          (json) {
            json['remaining'] = ['item-a'];
          },
          (json) {
            json['skipped'] = ['foreign'];
          },
          (json) {
            json['deleted'] = ['item-c', 'item-a'];
          },
        ];

        for (final invalidate in invalidCases) {
          final json = _emptyResultJson();
          invalidate(json);
          expect(
            () => TrashEmptyResult.fromJson(json, selection: selection),
            throwsFormatException,
            reason: json.toString(),
          );
        }
      },
    );

    test('rejects inconsistent counts, partial state, and warning type', () {
      final invalidCases = <void Function(Map<String, dynamic>)>[
        (json) => json['deleted_count'] = 1,
        (json) => json['remaining_count'] = 0,
        (json) => json['skipped_count'] = 0,
        (json) => json['partial'] = false,
        (json) => json['warning'] = 1,
      ];

      for (final invalidate in invalidCases) {
        final json = _emptyResultJson();
        invalidate(json);
        expect(
          () => TrashEmptyResult.fromJson(json, selection: selection),
          throwsFormatException,
          reason: json.toString(),
        );
      }
    });
  });
}

Map<String, dynamic> _listingJson() => {
  'items': [
    {
      'id': 'item-a',
      'originalPath': '/docs/report.txt',
      'deletedAt': '2026-07-19T12:00:00+08:00',
      'expiresAt': '2026-08-18T12:00:00+08:00',
      'name': 'report.txt',
      'isDir': false,
      'size': 42,
      'hadVersions': true,
    },
    {
      'id': 'item-b',
      'originalPath': '/photos/家庭相册',
      'deletedAt': '2026-07-19T12:30:00+08:00',
      'expiresAt': '2026-08-18T12:30:00+08:00',
      'name': '家庭相册',
      'isDir': true,
      'size': 0,
      'hadVersions': false,
    },
  ],
  'count': 2,
  'totalSize': 42,
  'retentionDays': 30,
  'retentionEnabled': true,
  'retentionMaxSize': 1024,
  'trashAutoCleanupEnabled': true,
};

Map<String, dynamic> _firstItem(Map<String, dynamic> json) {
  return (json['items']! as List<dynamic>).first as Map<String, dynamic>;
}

Map<String, dynamic> _secondItem(Map<String, dynamic> json) {
  return (json['items']! as List<dynamic>)[1] as Map<String, dynamic>;
}

Map<String, dynamic> _emptyResultJson() => {
  'deleted': ['item-a', 'item-c'],
  'remaining': ['item-b'],
  'skipped': ['item-d'],
  'deleted_count': 2,
  'remaining_count': 1,
  'skipped_count': 1,
  'partial': true,
  'warning': false,
};
