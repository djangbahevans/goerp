import { Slot } from "@radix-ui/react-slot";
import { createFileRoute } from "@tanstack/react-router";
import { motion } from "motion/react";
import type { ComponentProps } from "react";

export const Route = createFileRoute("/")({
  component: IndexPage,
});

function Surface({ asChild, ...props }: { asChild?: boolean } & ComponentProps<"div">) {
  const Comp = asChild ? Slot : "div";
  return <Comp {...props} />;
}

function IndexPage() {
  return (
    <Surface className="flex min-h-screen items-center justify-center bg-bg text-fg">
      <motion.p initial={{ opacity: 0 }} animate={{ opacity: 1 }}>
        GoERP Shell
      </motion.p>
    </Surface>
  );
}
