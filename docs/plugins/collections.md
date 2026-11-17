# File collections

A collection manifest declares a presentation/schema for the host's existing file-library primitives. It cannot declare arbitrary SQL, filesystem paths, custom frontend scripts or new metadata keys. Documents is the first official collection; it is not embedded as a mandatory Core feature.

The `collection` object contains:

| Field | Bound |
| --- | --- |
| `title`, `description` | Required nonempty title up to 120 bytes; description up to 1,000 bytes |
| `categories` | 1–32 unique `{value,label}` options; safe storage keys and nonempty labels up to 120 bytes |
| `mediaTypes` | 1–4 distinct entries from PDF, PNG, JPEG and UTF-8 plain text |
| `maxFileBytes` | 1 through 20 MiB |
| `titleMaxLength` | 1–1,024 Unicode characters |
| `descriptionMaxLength` | 1–16,000 Unicode characters |
| `maxTags` | 0–64 |
| `tagMaxLength` | 1–256 Unicode characters |
| `legacySnapshot` | Compatibility flag allowed only for Documents; leave absent for new collections |

The metadata schema is exactly `title`, `category`, `description`, `tags`. Categories must be declared. Text is trimmed/validated and tags deduplicated case-insensitively. Metadata is bounded at 16 KiB. A future field such as `purchaseDate` requires an explicit host contract evolution; accepting it in WASI alone does not add host persistence or rendering support.

Declare a `validate` view with a `metadata` field. The host supplies the metadata JSON as that string; the guest returns validated/normalized metadata JSON inside `text`. The host applies its own strict schema and limits afterward. Guest validation cannot bypass host authorization, content sniffing or limits.

The host implements upload, metadata editing/search, authorized download, explicit read sharing and backup/restore. Originals are private to their owner unless shared with a current household member. Removal/rejoining invalidates old recipient grants. The owner can include household-private records in a full encrypted backup.

Originals can use local disk or the household's configured S3 storage. Credentials stay host-side. A storage-setting change affects new uploads; moving existing originals is a separately confirmed operation with verified copies. Restore writes exported originals locally and requires the exact executable package separately.
