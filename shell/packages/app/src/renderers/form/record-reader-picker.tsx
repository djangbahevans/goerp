import { AuthContext } from "@goerp/sdk/auth";
import { Combobox, UserAvatar } from "@goerp/sdk/components";
import { type RecordReader, useRecordReaders } from "@goerp/sdk/react";
import { type ReactNode, useContext, useState } from "react";

// docs/components/record-reader-picker.md.
export interface RecordReaderPickerProps {
  model: string;
  recordId: string;
  value: RecordReader | null;
  onChange: (reader: RecordReader | null) => void;
  excludeSelf?: boolean | undefined;
  clearable?: boolean | undefined;
  id?: string | undefined;
  disabled?: boolean | undefined;
  placeholder?: string | undefined;
}

export function RecordReaderPicker({
  model,
  recordId,
  value,
  onChange,
  excludeSelf = false,
  clearable = true,
  id,
  disabled = false,
  placeholder = "Search people…",
}: RecordReaderPickerProps): ReactNode {
  const [query, setQuery] = useState("");
  const [open, setOpen] = useState(false);
  const viewerId = useContext(AuthContext)?.user?.id;
  const { readers, isLoading, isError } = useRecordReaders(model, recordId, open ? query : null, { excludeSelf });

  const displayName = (reader: RecordReader) => {
    const name = reader.name ?? reader.email;
    return reader.id === viewerId ? `${name} (you)` : name;
  };

  return (
    <Combobox<RecordReader>
      id={id}
      query={query}
      onQueryChange={setQuery}
      options={readers}
      getOptionKey={(reader) => reader.id}
      getOptionLabel={(reader) => (reader.name ? `${displayName(reader)}, ${reader.email}` : displayName(reader))}
      renderOption={(reader) => (
        <span className="flex min-w-0 items-center gap-2">
          <UserAvatar userId={reader.id} name={reader.name ?? reader.email} avatarUrl={reader.avatarUrl} size="xs" />
          <span className="flex min-w-0 flex-col">
            <span className="truncate text-sm text-text">
              {reader.name ?? reader.email}
              {reader.id === viewerId && <span className="text-text-secondary"> (you)</span>}
            </span>
            {reader.name && <span className="truncate text-text-secondary text-xs">{reader.email}</span>}
          </span>
        </span>
      )}
      onSelect={onChange}
      status={isError ? "error" : isLoading ? "loading" : "ready"}
      errorContent={<p className="px-2 py-1 text-danger text-sm">Couldn't load people.</p>}
      emptyContent={<p className="px-2 py-1 text-sm text-text-secondary">No one found who can see this record.</p>}
      selectedLabel={value ? (value.name ?? value.email) : undefined}
      onClear={clearable && value ? () => onChange(null) : undefined}
      onOpenChange={setOpen}
      disabled={disabled}
      placeholder={placeholder}
    />
  );
}
