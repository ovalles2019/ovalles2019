"""Langflow custom component: exposes the job-intel pipeline as a drag-and-drop node.

Use it to prototype visually: wire a File/Text input -> this node -> Chat Output,
then try prompt variations in Langflow's UI before committing them to code.

Setup (from the project root):
    pip install langflow -e .                    # job_intel must be importable
    LANGFLOW_COMPONENTS_PATH=$(pwd)/langflow langflow run
The node then appears under the "job_intel" category in the sidebar.

Imports differ across Langflow versions (newer releases ship the component API in
the `lfx` package), so both paths are tried.
"""

try:  # Langflow >= 1.5
    from lfx.custom.custom_component.component import Component
    from lfx.io import IntInput, MultilineInput, Output
    from lfx.schema.message import Message
except ImportError:  # older Langflow 1.x
    from langflow.custom import Component
    from langflow.io import IntInput, MultilineInput, Output
    from langflow.schema.message import Message


class JobIntelComponent(Component):
    display_name = "Job Intel"
    description = "LangExtract the posting, score fit with Claude, and tailor the resume."
    icon = "briefcase"

    inputs = [
        MultilineInput(name="posting", display_name="Job posting"),
        MultilineInput(name="resume", display_name="Resume"),
        MultilineInput(name="guidance", display_name="Tailoring guidance", value=""),
        IntInput(name="threshold", display_name="Fit threshold", value=60),
    ]
    outputs = [Output(display_name="Report", name="report", method="run")]

    def run(self) -> Message:
        import uuid

        from langgraph.types import Command

        from job_intel import observability
        from job_intel.graph import build_graph

        graph = build_graph(threshold=self.threshold)
        config = observability.run_config(thread_id=str(uuid.uuid4()))
        result = graph.invoke({"posting": self.posting, "resume": self.resume}, config)
        if result.get("__interrupt__"):
            # In the UI the human already approved by pressing Run; resume with their guidance.
            result = graph.invoke(Command(resume={"approved": True, "guidance": self.guidance}), config)
        observability.flush()
        self.status = result["report"][:200]
        return Message(text=result["report"])
