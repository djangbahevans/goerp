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

  return (
    <Combobox<RecordReader>
      id={id}
      query={query}
      onQueryChange={setQuery}
      options={readers}
      getOptionKey={(reader) => reader.id}
      getOptionLabel={(reader) => recordReaderOptionLabel(reader, viewerId)}
      renderOption={(reader) => <RecordReaderOption reader={reader} viewerId={viewerId} />}
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

// A reader's accessible option name: "{name}, {email}", or their email
// alone when they have no name, marked "(you)" for the viewer.
export function recordReaderOptionLabel(reader: RecordReader, viewerId: string | undefined): string {
  const name = reader.name ?? reader.email;
  const shown = reader.id === viewerId ? `${name} (you)` : name;
  return reader.name ? `${shown}, ${reader.email}` : shown;
}

// One reader's option row: avatar, name, and the email that tells two
// people with the same name apart. The mention list uses it too.
export function RecordReaderOption({ reader, viewerId }: { reader: RecordReader; viewerId: string | undefined }) {
  return (
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
  );
}
